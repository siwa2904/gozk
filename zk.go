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
	"time"
)

const (
	DefaultTimezone = "Asia/Shanghai"
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

var Log logger

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
	RtEventData  <-chan *DataMsg
	ResponseData <-chan *DataMsg
	lastCMD      int
	disabled     bool
	capturing    chan bool
	done         chan bool
	Log          logger
}

//machineID 机器 ID 用于识别唯一机器（可直接传0，由系统自动从机器上获取）
//host 机器IP
//port 端口 一般是 4370
//timezone 时区
func NewZK(machineID int, host string, port int, pin int, timezone string) *ZK {
	if Log == nil {
		Log = &gozkLogger{
			stdLog: log.New(os.Stdout, "", log.LstdFlags),
			errLog: log.New(os.Stderr, "", log.LstdFlags),
		}
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
		Log:       Log,
	}
}

func (zk *ZK) dataReceive() {
	RtEventData := make(chan *DataMsg, 20)
	ResponseData := make(chan *DataMsg)
	defer func() {
		close(RtEventData)
		close(ResponseData)
	}()

	zk.RtEventData = RtEventData
	zk.ResponseData = ResponseData

	for {
		select {
		case <-zk.done:
			zk.Log.Info(zk.machineID, "Exit dataReceive")
			return
		default:
			data := make([]byte, 1032)
			n, err := zk.conn.Read(data)

			if err != nil || n < 16 {
				if zk.lastCMD != CMD_REG_EVENT {
					ResponseData <- &DataMsg{
						Data: []byte(err.Error()),
						Head: ZkHead{
							Code: CMD_ACK_UNKNOWN,
						},
					}
				}

				if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
					continue
				}
				zk.Log.Errorf("[%d] 接收数据失败,%v", zk.machineID, err)
				//重新连接时会重置 zk.conn,这里等待重新连接成功后再继续。
				zk.reConnect()
				return
			}
			data = data[:n]

			zk.Log.Debugf("[%d] Response[RAW]: %v", zk.machineID, data[8:])

			header := mustUnpack([]string{"H", "H", "H", "H"}, data[8:16])
			msg := &DataMsg{}
			msg.Head.Code = header[0].(int)
			msg.Head.CheckSum = header[1].(int)
			msg.Head.SessionID = header[2].(int)
			msg.Head.ReplyID = header[3].(int)
			msg.Data = data[16:]
			zk.Log.Debug(zk.machineID, "Response[USER]:", msg)
			if msg.Head.Code == CMD_REG_EVENT {
				RtEventData <- msg
			} else {
				ResponseData <- msg
			}
		}
	}
}

//重新连接
func (zk *ZK) reConnect() {
	connTimes := 0
	for {
		time.Sleep(ReadSocketTimeout)
		select {
		case <-zk.done:
			return
		default:
			connTimes++
			if connTimes >= 20 { //重试超过20次等待一分钟再试
				time.Sleep(time.Minute)
			}
			zk.Log.Info(zk.machineID, "检查到连接断开,正在尝试重新连接...", connTimes)
			zk.conn = nil
			err := zk.Connect()
			if err != nil {
				zk.Log.Error(zk.machineID, "连接失败", err)

				continue
			}
			zk.Log.Info(zk.machineID, "重新连接成功", zk.sessionID)
			if zk.capturing != nil {
				if err := zk.regEvent(EF_ATTLOG); err != nil {
					zk.Log.Error(zk.machineID, "激活实时事件失败", err)
					close(zk.capturing)
				} else {
					zk.Log.Info(zk.machineID, "重新激活实时事件成功")
				}
			}
			return
		}
	}
}

//todo 当 machineID＝0 时，连接成功后自动从机器上获取 ID
func (zk *ZK) Connect() (err error) {
	if zk.conn != nil {
		zk.Log.Error(zk.machineID, "Already connected")
		return nil
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", zk.host, zk.port), 3*time.Second)
	if err != nil {
		return err
	}

	tcpConnection := conn.(*net.TCPConn)
	if err = tcpConnection.SetKeepAlive(true); err != nil {
		return err
	}

	if err = tcpConnection.SetKeepAlivePeriod(KeepAlivePeriod); err != nil {
		return err
	}

	zk.conn = tcpConnection

	go zk.dataReceive()

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
			return errors.New("unauthorized")
		}
	}
	zk.Log.Info(zk.machineID, "Connected with session_id", zk.sessionID)
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
	zk.Log.Debugf("[%d] DataSend[RAW]: %v", zk.machineID, header)
	zk.Log.Debugf("[%d] DataSend[USER]: CMD:%d%v,SessionID:%d ReplayID:%d", zk.machineID, command, commandString, zk.sessionID, zk.replyID)

	if n, err := zk.conn.Write(top); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, errors.New("Failed to write command")
	}

	msg := <-zk.ResponseData

	if msg.Head.Code == CMD_ACK_UNKNOWN {
		return nil, fmt.Errorf("GOT ERROR %s ON COMMAND %d", msg.Data, command)
	}

	dataReceived := msg.Data
	tcpLength := testTCPTop(dataReceived)

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
		}, nil
	}
}

// Disconnect disconnects out of the machine fingerprint
func (zk *ZK) Disconnect() error {
	if zk.conn == nil {
		return errors.New("Already disconnected")
	}

	if _, err := zk.sendCommand(CMD_EXIT, nil, 8); err != nil {
		return err
	}
	close(zk.done)
	if err := zk.conn.Close(); err != nil {
		return err
	}

	zk.conn = nil
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
	if err := zk.GetUsers(); err != nil {
		return nil, err
	}

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

	return attendances, nil
}

// GetUsers returns a list of users
// For now, just run this func. I'll implement this function later on.
func (zk *ZK) GetUsers() error {

	_, err := zk.readSize()
	if err != nil {
		return err
	}

	_, size, err := zk.readWithBuffer(CMD_USERTEMP_RRQ, FCT_USER, 0)
	if err != nil {
		return err
	}

	if size < 4 {
		return nil
	}

	return nil
}

func (zk *ZK) LiveCapture(chanAttendance chan<- *Attendance) error {
	if zk.capturing != nil {
		return errors.New("Is capturing")
	}

	if zk.disabled {
		if err := zk.EnableDevice(); err != nil {
			return err
		}
	}

	if err := zk.regEvent(EF_ATTLOG); err != nil {
		return err
	}

	zk.Log.Info(zk.machineID, "Start capturing")
	zk.capturing = make(chan bool)

	go func() {
		defer func() {
			zk.Log.Debug(zk.machineID, "停止实时事件接收!")
			close(zk.capturing)
			zk.capturing = nil
		}()
		for {
			select {
			case <-zk.capturing:
				zk.Log.Debug("接收到 capturing 信号")
				return
			case <-zk.done:
				zk.Log.Debug("接收到 done 信号")
				return
			case msg, ok := <-zk.RtEventData:
				if !ok {
					time.Sleep(time.Second)
					continue
				}
				data := msg.Data
				for len(data) >= 12 {
					unpack := []interface{}{}

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

					timestamp := zk.decodeTimeHex([]byte(unpack[3].(string)))

					userID, err := strconv.ParseInt(strings.Replace(unpack[0].(string), "\x00", "", -1), 10, 64)
					if err != nil {
						zk.Log.Error(err)
						continue
					}
					attLog := &Attendance{UserID: userID, AttendedAt: timestamp, VerifyMethod: uint(unpack[1].(int)), SensorID: zk.machineID}
					zk.Log.Debug(zk.machineID, "打卡记录", attLog)

					chanAttendance <- attLog
				}
			}
		}
	}()

	return nil
}

func (zk ZK) StopCapture() {
	if zk.capturing == nil {
		return
	}
	zk.Log.Info(zk.machineID, "Stopping capturing")
	zk.regEvent(0)
	zk.capturing <- false
	<-zk.capturing
	zk.Log.Info(zk.machineID, "Stopped capturing")
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
