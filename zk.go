package gozk

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTimezone = "Asia/Bangkok"
)

var (
	KeepAlivePeriod   = time.Second * 6
	ReadSocketTimeout = 3 * time.Second
)

type logger interface {
	Info(v ...interface{})
	Infof(format string, v ...interface{})
	Debug(v ...interface{})
	Debugf(format string, v ...interface{})
	Error(v ...interface{})
	Errorf(format string, v ...interface{})
}

// AttLogFunc ฟังก์ชันการประมวลผลบันทึกการเช็คอินตามเวลาจริง
type AttLogFunc func(attendance *Attendance)

type ZK struct {
	conn         *net.TCPConn
	sessionID    int
	replyID      int
	host         string
	port         int
	pin          int
	loc          *time.Location
	machineID    int
	lastData     []byte
	ResponseData chan *DataMsg
	eventData    chan *DataMsg
	lastCMD      int
	disabled     bool
	capturing    chan bool
	done         chan bool
	Log          logger
	lck          sync.RWMutex
	attLogFunc   AttLogFunc
}

//รหัสเครื่อง หมายเลขเครื่องใช้เพื่อระบุเครื่องที่ไม่ซ้ำกัน (สามารถผ่าน 0 ได้โดยตรง และระบบจะรับรหัสจากเครื่องโดยอัตโนมัติ)
// IP เครื่องโฮสต์
// port โดยทั่วไปคือ 4370
// เขตเวลา เขตเวลา

func NewZK(machineID int, host string, port int, pin int, timezone string) *ZK {
	prefix := fmt.Sprintf("[%03d]", machineID)
	log := &gozkLogger{
		stdLog: log.New(os.Stdout, prefix, log.LstdFlags),
		errLog: log.New(os.Stderr, prefix, log.LstdFlags),
	}
	return &ZK{
		machineID: machineID,
		host:      host,
		port:      port,
		pin:       pin,
		loc:       LoadLocation(timezone),
		sessionID: 0,
		replyID:   USHRT_MAX - 1,
		done:      make(chan bool),
		Log:       log,
		eventData: make(chan *DataMsg, 20),
		attLogFunc: func(attLog *Attendance) {

		},
	}
}

func (zk *ZK) connRead(conn *net.TCPConn) (msg *DataMsg, err error) {
	var n int
	data := make([]byte, 1032)
	n, err = conn.Read(data)
	if err != nil || n < 16 {
		if zk.lastCMD != CMD_REG_EVENT {
			msg = &DataMsg{
				Data: []byte(err.Error()),
				Head: ZkHead{
					Code: CMD_ACK_UNKNOWN,
				},
			}
		}
		return
	}

	data = data[:n]
	// zk.Log.Debugf("Response[RAW]: %v", data[8:])
	header := mustUnpack([]string{"H", "H", "H", "H"}, data[8:16])
	msg.Head.Code = header[0].(int)
	msg.Head.CheckSum = header[1].(int)
	msg.Head.SessionID = header[2].(int)
	msg.Head.ReplyID = header[3].(int)
	msg.Data = data[16:]
	// zk.Log.Debug("Response[USER]:", msg)
	return
}

func (zk *ZK) dataReceive() {
	defer func() {
		close(zk.ResponseData)
	}()
	connTimes := 0
	testConn := make(chan bool, 1)
	for {
		select {
		case <-zk.done:
			zk.Log.Info(zk.host + ": หยุดรับข้อมูล")
			return
		case <-testConn:
			connTimes += 1
			zk.Log.Info(zk.host+": ลองอีกครั้งหลังจาก 15 วินาทีที่การเชื่อมต่อล้มเหลว", connTimes)
			for i := 0; i <= connTimes/10; i++ {
				<-time.After(time.Second * 5)
			}
			conn, e := zk.TestConnect()
			if e != nil {
				testConn <- true
			} else {
				_ = conn.Close()
				err := zk.Disconnect()
				if err != nil {
					zk.Log.Error(zk.host+": ตัดการเชื่อมต่อล้มเหลว", err)
				}
				zk.lck.Lock()
				if zk.conn != nil {
					_ = zk.conn.Close()
				}
				zk.conn = nil
				zk.lck.Unlock()
				go zk.reConnect()
			}
		default:
			zk.lck.RLock()
			conn := zk.conn
			zk.lck.RUnlock()
			if conn == nil {
				<-time.After(time.Second * 1)
				continue
			}

			data := make([]byte, 1032)
			conn.SetReadDeadline(time.Now().Add(time.Minute))
			n, err := conn.Read(data)
			zk.Log.Debug(zk.host+": รับข้อมูล", n, zk.lastCMD, err)
			if err != nil || n < 16 {
				if zk.lastCMD != CMD_REG_EVENT && zk.lastCMD != 0 {
					zk.ResponseData <- &DataMsg{
						Data: []byte(err.Error()),
						Head: ZkHead{
							Code: CMD_ACK_UNKNOWN,
						},
					}
				}

				if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
					zk.Log.Debug(zk.host + ": Timeout")
					go func() {
						_, e := zk.sendCommand(CMD_GET_TIME, nil, 8)
						if e != nil {
							zk.Log.Debug(zk.host+": ผิดพลาด", e)
						}
					}()
					continue
				}
				zk.Log.Error(zk.host+": ไม่สามารถรับข้อมูลได้", err)
				zk.attLogFunc(&Attendance{UserID: -1, AttendedAt: time.Now(), IsInValid: true, VerifyMethod: 0, SensorID: zk.machineID})
				connTimes = 0
				testConn <- true
				continue
			}

			if connTimes > 0 {
				connTimes = 0
				zk.attLogFunc(&Attendance{UserID: -2, AttendedAt: time.Now(), IsInValid: true, VerifyMethod: 0, SensorID: zk.machineID})
			}

			data = data[:n]

			// zk.Log.Debugf("Response[RAW]: %v", data[8:])

			header := mustUnpack([]string{"H", "H", "H", "H"}, data[8:16])
			msg := DataMsg{}
			msg.Head.Code = header[0].(int)
			msg.Head.CheckSum = header[1].(int)
			msg.Head.SessionID = header[2].(int)
			msg.Head.ReplyID = header[3].(int)
			msg.Data = data[16:]

			datas := mustUnpack([]string{"H", "H", "H", "H"}, data[:16])
			fmt.Println(zk.host+": Datasssss::", datas)
			// zk.Log.Debug("Response[USER]:", msg)
			if msg.Head.Code == CMD_REG_EVENT {
				zk.processEvent(msg)
			} else {
				if zk.lastCMD == CMD_GET_TIME {
					t, e := zk.decodeTime(msg.Data)
					zk.Log.Debug(zk.host+": เวลาปัจจุบัน", t, e)
				}
				zk.ResponseData <- &msg
			}
		}
	}
}

// เชื่อมต่อใหม่
func (zk *ZK) reConnect() {
	connTimes := 0

	for {
		time.Sleep(ReadSocketTimeout)
		select {
		case <-zk.done:
			return
		default:

			connTimes++
			if connTimes >= 20 { //ลองใหม่มากกว่า 20 ครั้งและรอหนึ่งนาทีเพื่อลองอีกครั้ง
				time.Sleep(time.Minute)
			}

			zk.Log.Info("ตรวจสอบว่าการเชื่อมต่อถูกตัดการเชื่อมต่อ พยายามเชื่อมต่อใหม่...", connTimes)

			err := zk.Connect()
			if err != nil {
				zk.Log.Error("การเชื่อมต่อล้มเหลว", err)
				continue
			}
			zk.Log.Info("เชื่อมต่อใหม่เรียบร้อยแล้ว", zk.sessionID)
			if zk.capturing != nil {
				if err := zk.regEvent(EF_ATTLOG); err != nil {
					zk.Log.Error("ลงทะเบียนรับ Event การลงเวลาไม่สำเร็จ", err)
				} else {
					zk.Log.Info("ลงทะเบียนรับ Event การลงเวลาสำเร็จ")
				}
			}
			return
		}
	}
}

func (zk *ZK) TestConnect() (conn net.Conn, err error) {
	conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", zk.host, zk.port), 3*time.Second)
	return
}

// สิ่งที่ต้องทำ เมื่อ machineID=0 รับ ID จากเครื่องโดยอัตโนมัติหลังจากการเชื่อมต่อสำเร็จ
func (zk *ZK) Connect() (err error) {
	zk.Log.Info(zk.host + ": เริ่มการเชื่อมต่อ")
	zk.lck.RLock()
	tcpConnection := zk.conn
	zk.lck.RUnlock()

	if tcpConnection != nil {
		zk.Log.Error(zk.host+": เชื่อมต่อ", zk.sessionID)
		return nil
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", zk.host, zk.port), 3*time.Second)
	if err != nil {
		return err
	}

	tcpConnection = conn.(*net.TCPConn)
	if err = tcpConnection.SetKeepAlive(true); err != nil {
		return err
	}

	if err = tcpConnection.SetKeepAlivePeriod(KeepAlivePeriod); err != nil {
		return err
	}

	zk.lck.Lock()
	zk.conn = tcpConnection
	zk.sessionID = 0
	zk.replyID = USHRT_MAX - 1
	zk.lck.Unlock()

	//การเชื่อมต่อหลายรายการเรียกใช้พื้นหลังการรับข้อมูลเพียงครั้งเดียว
	if zk.ResponseData == nil {
		zk.ResponseData = make(chan *DataMsg)
		go zk.dataReceive()
	}

	res, err := zk.sendCommand(CMD_CONNECT, nil, 8)
	if err != nil {
		return err
	}

	zk.sessionID = res.CommandID

	if res.Code == CMD_ACK_UNAUTH {
		commandString, _ := makeCommKey(zk.pin, zk.sessionID, 50)
		res, err := zk.sendCommand(CMD_AUTH, commandString, 8)
		if err != nil {
			return err
		}

		if !res.Status {
			return errors.New(zk.host + ": unauthorized")
		}
	}
	zk.Log.Info(zk.host+": การเชื่อมต่อสำเร็จ sessionID", zk.sessionID)
	return nil
}

func (zk *ZK) sendCommand(command int, commandString []byte, responseSize int) (*Response, error) {

	if commandString == nil {
		commandString = make([]byte, 0)
	}

	header, err := createHeader(command, commandString, zk.sessionID, zk.replyID)
	if err != nil {
		return nil, err
	}

	top, err := createTCPTop(header)
	if err != nil && err != io.EOF {
		return nil, err
	}

	zk.lastCMD = command
	zk.Log.Debugf("DataSend[RAW]: %v", header)
	zk.Log.Debugf("DataSend[USER]: CMD:%d%v,SessionID:%d ReplayID:%d", command, commandString, zk.sessionID, zk.replyID)

	if n, err := zk.conn.Write(top); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, errors.New("Failed to write command")
	}

	msg := <-zk.ResponseData
	zk.lastCMD = 0
	if msg.Head.Code == CMD_ACK_UNKNOWN {
		return nil, fmt.Errorf("GOT ERROR %s ON COMMAND %d", msg.Data, command)
	}
	// // this will work

	dataReceived := msg.Data
	tcpLength := testTCPTop(dataReceived)
	// fmt.Println(tcpLength)
	zk.replyID = msg.Head.ReplyID
	zk.lastData = dataReceived

	switch msg.Head.Code {
	case CMD_ACK_OK, CMD_PREPARE_DATA, CMD_DATA:
		return &Response{
			Status:    true,
			Code:      msg.Head.Code,
			TCPLength: tcpLength,
			CommandID: msg.Head.SessionID,
		}, nil
	default:
		return &Response{
			Status:    false,
			Code:      msg.Head.Code,
			TCPLength: tcpLength,
			CommandID: msg.Head.SessionID,
			Data:      dataReceived,
		}, nil
	}
}

// Disconnect disconnects out of the machine fingerprint
func (zk *ZK) Disconnect() error {
	zk.Log.Debug("Disconnect", zk.sessionID)
	zk.lck.RLock()
	conn := zk.conn
	zk.lck.RUnlock()
	if conn == nil {
		return nil
	}

	if _, err := zk.sendCommand(CMD_EXIT, nil, 8); err != nil {
		return err
	}
	close(zk.done)
	if err := conn.Close(); err != nil {
		return err
	}
	zk.lck.Lock()
	zk.conn = nil
	zk.lck.Unlock()

	zk.Log.Debug("ตัดการเชื่อมต่อ", zk.sessionID)
	return nil
}

// EnableDevice enables the connected device
func (zk *ZK) EnableDevice() error {

	res, err := zk.sendCommand(CMD_ENABLEDEVICE, nil, 8)
	if err != nil {
		return err
	}

	if !res.Status {
		return errors.New("Failed to enable device")
	}

	zk.disabled = false
	return nil
}

// DisableDevice disable the connected device
func (zk *ZK) DisableDevice() error {
	res, err := zk.sendCommand(CMD_DISABLEDEVICE, nil, 8)
	if err != nil {
		return err
	}

	if !res.Status {
		return errors.New("Failed to disable device")
	}

	zk.disabled = true
	return nil
}

// GetAttendances returns a list of attendances
func (zk *ZK) GetAttendances() ([]*Attendance, error) {
	// if err := zk.GetUsers(); err != nil {
	// 	fmt.Println("GetUsersErr2::", err)
	// 	return nil, err
	// }

	records, err := zk.readSize()
	if err != nil {
		return nil, err
	}

	data, size, err := zk.readWithBuffer(CMD_ATTLOG_RRQ, 0, 0)
	if err != nil {
		return nil, err
	}

	if size < 4 {
		return []*Attendance{}, nil
	}

	totalSizeByte := data[:4]
	data = data[4:]

	totalSize := mustUnpack([]string{"I"}, totalSizeByte)[0].(int)
	recordSize := totalSize / records
	attendances := []*Attendance{}

	if recordSize == 8 || recordSize == 16 {
		return nil, errors.New("Sorry I don't support this kind of device. I'm lazy")

	}

	for len(data) >= 40 {

		v, err := newBP().UnPack([]string{"H", "24s", "B", "4s", "B", "8s"}, data[:40])
		if err != nil {
			return nil, err
		}
		timestamp, err := zk.decodeTime([]byte(v[3].(string)))
		if err != nil {
			return nil, err
		}

		userID, err := strconv.ParseInt(strings.Replace(v[1].(string), "\x00", "", -1), 10, 64)
		if err != nil {
			return nil, err
		}

		attendances = append(attendances, &Attendance{AttendedAt: timestamp, UserID: userID, SensorID: zk.machineID})
		data = data[40:]
	}

	fmt.Println("attendances", attendances)

	return attendances, nil
}

func (zk *ZK) ClearAttendance() error {
	_, err := zk.sendCommand(CMD_CLEAR_ATTLOG, nil, 8)
	if err != nil {
		return err
	}
	return nil
}

// GetUsers returns a list of users
// For now, just run this func. I'll implement this function later on.
func (zk *ZK) GetUsers() ([]*User, error) {
	records, err := zk.readSize()
	if err != nil {
		fmt.Println("GetUsersErr::::", err)
		return nil, err
	}
	userdata, size, err := zk.readWithBuffer(CMD_USERTEMP_RRQ, FCT_USER, 0)
	if err != nil {
		fmt.Println("GetUsersErr0::", err)
		return nil, err
	}
	if size < 4 {
		fmt.Println("GetUsersErr5::", err)
		return []*User{}, nil
	}
	totalSizeByte := userdata[:4]
	userdata = userdata[4:]
	totalSize := mustUnpack([]string{"I"}, totalSizeByte)[0].(int)
	recordSize := totalSize / records

	user := []*User{}
	if recordSize == 28 {
		for len(userdata) >= 28 {
			v, err := newBP().UnPack([]string{"H", "B", "5s", "8s", "s", "I", "x", "B", "h", "I"}, userdata[:28])
			if err != nil {
				fmt.Println("GetUsersErr::", err)
				return nil, err
			}
			userID, err := strconv.ParseInt(strings.Replace(v[2].(string), "\x00", "", -1), 10, 64)
			if err != nil {
				fmt.Println("GetUsersErr2::", err)
				return nil, err
			}
			user = append(user, &User{UID: userID, UserID: v[2].(string), Name: v[3].(string), Privilege: v[4].(int), Password: v[5].(string), GroupID: v[6].(int), Card: v[7].(string)})
			userdata = userdata[28:]
		}
	}

	fmt.Println("data", userdata)
	return user, nil
}

func (zk *ZK) SetUser(user User) error {

	// cd, err := iconv.Open("tis-620", "utf-8") // convert utf-8 to gbk
	// cd, err := iconv.ConvertString(user.Name, "utf-8", "tis-620")
	// if err != nil {
	// 	fmt.Println("iconv.Open failed!")
	// 	return err
	// }
	// defer cd.Close()
	// userTh := cd
	// user.Name = userTh
	commandString, err := makeUserCommand(user)
	if err != nil {
		fmt.Println(err)
		return err
	}
	res, err := zk.sendCommand(CMD_USER_WRQ, commandString, 50)
	if err != nil {
		fmt.Println(zk.host+": AddErr::", err)
		return err
	}
	zk.Log.Info(zk.host+": Add Resp ", res)
	return nil
}

func (zk *ZK) GetUserTemp(uid int, temp_id int, user_id string) error {
	fmt.Println("GetUserTemp", uid, temp_id)
	commandString, err := makeGetUsertTmplateCommand(uid, temp_id)

	if err != nil {
		fmt.Println(err)
		return err
	}
	res, err := zk.sendCommand(CMD_GET_USERTEMP, commandString, (1024 + 8))
	if err != nil {
		fmt.Println(zk.host+": GetErr::", err)
		return err
	}
	zk.Log.Info(zk.host+": Get Resp:: ", res.Status)
	return nil
}

func (zk *ZK) processEvent(msg DataMsg) {
	data := msg.Data

	//อาจมีบันทึกหลายรายการในข้อมูลหนึ่งๆ ซึ่งจะถูกประมวลผลโดยตรงในลูปที่นี่
	for len(data) >= 12 {
		var unpack []interface{}
		fmt.Println(data)
		if len(data) == 12 {
			unpack = mustUnpack([]string{"I", "B", "B", "6s"}, data)
			data = data[12:]
		} else if len(data) == 32 {
			unpack = mustUnpack([]string{"24s", "B", "B", "6s"}, data[:32])
			data = data[32:]
		} else if len(data) == 36 {
			unpack = mustUnpack([]string{"24s", "B", "B", "6s", "4s"}, data[:36])
			data = data[36:]
		} else if len(data) >= 52 {
			unpack = mustUnpack([]string{"24s", "B", "B", "6s", "20s"}, data[:52])
			data = data[52:]
		}

		fmt.Println("set_0", unpack[0].(string))
		fmt.Println("set_1", strconv.Itoa(unpack[1].(int)))
		fmt.Println("set_2", strconv.Itoa(unpack[2].(int)))
		fmt.Println("set_3", unpack[3].(string))

		timestamp := zk.decodeTimeHex([]byte(unpack[3].(string)))

		userID, err := strconv.ParseInt(strings.Replace(unpack[0].(string), "\x00", "", -1), 10, 64)
		if err != nil {
			zk.Log.Error(err)
			continue
		}

		attLog := &Attendance{UserID: userID, AttendedAt: timestamp, VerifyMethod: uint(unpack[1].(int)), SensorID: zk.machineID}
		zk.Log.Debug(zk.host+": บันทึกลงเวลา", attLog)
		zk.attLogFunc(attLog)
	}
}

func (zk *ZK) LiveCapture(logFunc AttLogFunc) error {
	if zk.capturing != nil {
		return errors.New(zk.host + ": Is capturing")
	}

	if zk.disabled {
		if err := zk.EnableDevice(); err != nil {
			return err
		}
	}

	if err := zk.regEvent(EF_ATTLOG); err != nil {
		return err
	}

	zk.Log.Info(zk.host + ": เริ่มการรับเหตุการณ์ Realtime")
	zk.capturing = make(chan bool)
	zk.attLogFunc = logFunc
	return nil
}
func (zk *ZK) GetTime() (time.Time, error) {
	res, err := zk.sendCommand(CMD_GET_TIME, nil, 1032)
	if err != nil {
		return time.Now(), err
	}
	if !res.Status {
		return time.Now(), errors.New("can not get time")
	}

	return zk.decodeTime(res.Data[:4])
}

func (zk *ZK) SetTime(settime time.Time) error {
	// zk.encodeTime()
	format := []string{"I"}
	values := []interface{}{zk.encodeTime(settime)}
	bpData, err := newBP().Pack(format, values)
	if err != nil {
		return err
	}
	rep, err := zk.sendCommand(CMD_SET_TIME, bpData, 1032)
	if err != nil {
		return err
	}
	fmt.Println("SetTime", rep)
	return nil
}

func (zk ZK) StopCapture() {
	if zk.capturing == nil {
		return
	}
	zk.Log.Info(zk.host + ": Stopping capturing")
	zk.regEvent(0)
	close(zk.capturing)
	zk.Log.Info(zk.host + ": Stopped capturing")
}

func (zk ZK) Clone() *ZK {
	return &ZK{
		host:      zk.host,
		port:      zk.port,
		pin:       zk.pin,
		loc:       zk.loc,
		sessionID: 0,
		replyID:   USHRT_MAX - 1,
	}
}

func (zk *ZK) GetAttendance() error {

	si, err := zk.readSize()
	if err != nil {
		return err
	}
	fmt.Println("si", si)
	data, size, err := zk.readWithBuffer(CMD_ATTLOG_RRQ, 0, 0)
	if err != nil {
		fmt.Println("GetAttErr::", err)
		return err
	}

	if size < 4 {
		fmt.Println("GetAttErr<4::", size)
		return nil
	}
	// []string{"I"}, zk.lastData[1:5]
	// total_size := newBP().UnPack([]string{"I"}, data[4])[0]
	fmt.Println("Attdata", data)
	return nil
}

func (zk *ZK) GetTemplates() error {
	// templates := []map[string]interface{}{}
	fmt.Println("GetTemplates")
	_, err := zk.readSize()
	if err != nil {
		fmt.Println("GetTempErr00::", err)
		return err
	}

	templatedata, size, err := zk.readWithBuffer(CMD_DB_RRQ, FCT_FINGERTMP, 0)
	if err != nil {
		fmt.Println("GetTempErr::", err)
		return err
	}
	if size < 4 {
		fmt.Println("WRN: no user data")
		return nil
	}
	ts, err2 := newBP().UnPack([]string{"I"}, templatedata[0:4])
	if err2 != nil {
		fmt.Println("GetTempErr::", err2)
		return err2
	}
	totalSize := ts[0].(int)
	fmt.Println("get template total size ?, size ? len ?", totalSize, size, len(templatedata))

	return nil
}
