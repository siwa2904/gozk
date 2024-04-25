package gozk

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	binarypack "github.com/canhlinh/go-binary-pack"
)

// PrintlHex printls bytes to console as HEX encoding
func PrintlHex(title string, buf []byte) {
	fmt.Printf("%s %q\n", title, hex.EncodeToString(buf))
}

func LoadLocation(timezone string) *time.Location {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Local
	}

	return location
}

func newBP() *binarypack.BinaryPack {
	return &binarypack.BinaryPack{}
}

func createCheckSum(p []interface{}) ([]byte, error) {
	l := len(p)
	checksum := 0

	for l > 1 {
		pack, err := newBP().Pack([]string{"B", "B"}, []interface{}{p[0], p[1]})
		if err != nil {
			return nil, err
		}

		unpack, err := newBP().UnPack([]string{"H"}, pack)
		if err != nil {
			return nil, err
		}

		c := unpack[0].(int)
		checksum += c
		p = p[2:]

		if checksum > USHRT_MAX {
			checksum -= USHRT_MAX
		}
		l -= 2
	}

	if l > 0 {
		checksum = checksum + p[len(p)-1].(int)
	}

	for checksum > USHRT_MAX {
		checksum -= USHRT_MAX
	}

	checksum = ^checksum
	for checksum < 0 {
		checksum += USHRT_MAX
	}

	return newBP().Pack([]string{"H"}, []interface{}{checksum})
}

func createHeader(command int, commandString []byte, sessionID int, replyID int) ([]byte, error) {
	buf, err := newBP().Pack([]string{"H", "H", "H", "H"}, []interface{}{command, 0, sessionID, replyID})
	if err != nil {
		return nil, err
	}
	buf = append(buf, commandString...)

	unpackPad := []string{
		"B", "B", "B", "B", "B", "B", "B", "B",
	}

	for i := 0; i < len(commandString); i++ {
		unpackPad = append(unpackPad, "B")
	}

	unpackBuf, err := newBP().UnPack(unpackPad, buf)
	if err != nil {
		return nil, err
	}

	checksumBuf, err := createCheckSum(unpackBuf)
	if err != nil {
		return nil, err
	}

	c, err := newBP().UnPack([]string{"H"}, checksumBuf)
	if err != nil {
		return nil, err
	}
	checksum := c[0].(int)

	replyID++
	if replyID >= USHRT_MAX {
		replyID -= USHRT_MAX
	}

	packData, err := newBP().Pack([]string{"H", "H", "H", "H"}, []interface{}{command, checksum, sessionID, replyID})
	if err != nil {
		return nil, err
	}

	return append(packData, commandString...), nil
}

func createTCPTop(packet []byte) ([]byte, error) {
	top, err := newBP().Pack([]string{"H", "H", "I"}, []interface{}{MACHINE_PREPARE_DATA_1, MACHINE_PREPARE_DATA_2, len(packet)})
	if err != nil {
		return nil, err
	}

	return append(top, packet...), nil
}

func testTCPTop(packet []byte) int {
	if len(packet) <= 8 {
		return 0
	}
	tcpHeader, err := newBP().UnPack([]string{"H", "H", "I"}, packet[:8])

	if err != nil {
		return 0
	}
	if tcpHeader[0].(int) == 13876 || tcpHeader[0].(int) == MACHINE_PREPARE_DATA_1 || tcpHeader[1].(int) == MACHINE_PREPARE_DATA_2 {

		return tcpHeader[2].(int)
	}

	return 0
}

func MakeCommonKey(key, sessionid int, ticks int) ([]byte, error) {
	return makeCommKey(key, sessionid, ticks)
}

// by chenall 原版 makeCommKey1 的算法无法正常使用，这个是根据网上的资料重写的。
func makeCommKey(key, sessionid int, ticks int) ([]byte, error) {
	k := 0
	//二进制反转
	for i := 31; i >= 0 && key != 0; i-- {
		k |= key & 1 << i
		key >>= 1
	}
	k += sessionid
	k ^= 0x4f534b5a //ZKSO
	k = (k&0xffff)<<16 | k>>16
	k = (k & 0xFF00FFFF) ^ (ticks | ticks<<8 | (ticks << 16) | (ticks << 24))
	return newBP().Pack([]string{"I"}, []interface{}{k})
}

func makeUserCommand(user User) ([]byte, error) {

	nn, _ := strconv.Atoi(user.Card)
	cardNo, er := newBP().Pack([]string{"I"}, []interface{}{nn})
	if er != nil {
		fmt.Println("cardNo Error:: ", er)
	}

	format := []string{"H", "B", "8s", "24s", "10s", "B", "H", "9s", "15s"}
	values := []interface{}{user.UID, user.Privilege, user.Password, user.Name, string(cardNo), user.GroupID, 0, user.UserID, ""}
	bpData, err := newBP().Pack(format, values)
	if err != nil {
		fmt.Println("makeUserCommand Error:: ", err)
	}
	return bpData, err
}

func makeGetUsertTmplateCommand(uid int, temp int) ([]byte, error) {

	format := []string{"H", "B"}
	values := []interface{}{uid, temp}
	bpData, err := newBP().Pack(format, values)
	if err != nil {
		fmt.Println("makeGetUsertTmplateCommand Error:: ", err)
	}
	fmt.Println("makeGetUsertTmplateCommand:: ", bpData)
	return bpData, err
}
func mustUnpack(pad []string, data []byte) []interface{} {
	value, err := newBP().UnPack(pad, data)
	if err != nil {
		panic(err)
	}

	return value
}

func getDataSize(rescode int, data []byte) (int, error) {
	if rescode == CMD_PREPARE_DATA {
		sizeUnpack, err := newBP().UnPack([]string{"I"}, data[:4])
		if err != nil {
			return 0, err
		}
		fmt.Println("getDataSize:: ", sizeUnpack[0].(int))
		return sizeUnpack[0].(int), nil
	}

	return 0, nil
}
