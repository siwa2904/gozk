package gozk

import (
	"fmt"
	"time"
)

type Response struct {
	Status    bool
	Code      int
	TCPLength int
	CommandID int
	Data      []byte
	ReplyID   int
}

type User struct {
	UID       int
	UserID    string
	Name      string
	Privilege int
	Password  string
	GroupID   int
	Card      string
}

type Attendance struct {
	UserID       int64     //EnrollNumber:用户编号
	AttendedAt   time.Time //Year, Month, Day, Hour, Minute, Second:年月日时分秒。
	IsInValid    bool      // 0 记录无效，1 记录有效。在指纹门禁机组拒绝或时间段原因拒绝开门时，该变量会返回无效值。
	AttState     uint      //考勤状态，表示 Checkin checkOut 等，值的范围为 0-5。超出无效。
	VerifyMethod uint      //比对方式，0，密码。1，指纹验证。
	SensorID     int
	UUID         int64 //传感器 ID ,机器 ID
}

type ZkHead struct {
	Code      int //命令或返回的代码
	CheckSum  int //校验码
	SessionID int
	ReplyID   int
}

type DataMsg struct {
	Head ZkHead
	Data []byte
}

func (r Response) String() string {
	return fmt.Sprintf("Status %v Code %d", r.Status, r.Code)
}
