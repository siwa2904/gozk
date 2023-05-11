package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/siwa2904/gozk"
)

func main() {
	zkSocket := gozk.NewZK(0, "192.168.1.253", 4370, 0, gozk.DefaultTimezone)
	if err := zkSocket.Connect(); err != nil {
		panic(err)
	}

	// c := make(chan *gozk.Attendance, 50)

	if err := zkSocket.LiveCapture(myLogFunc); err != nil {
		panic(err)
	}

	go RepleTime(zkSocket)
	// zkSocket.GetUsers()

	// dateString := "2023-05-03 16:59:00"
	// //convert string to time.Time
	// layout := "2006-01-02 15:04:05"
	// t, err := time.Parse(layout, dateString)
	// if err != nil {
	// 	fmt.Println(err)
	// }

	// zkSocket.SetTime(time.Now())
	// zkSocket.GetUserTemp(2, 50, "2")
	// zkSocket.GetUserTemp(2, 1, "2")
	// zkSocket.GetUserTemp(2, 2, "2")
	// zkSocket.GetUserTemp(2, 3, "2")
	// zkSocket.GetUserTemp(2, 4, "2")
	// zkSocket.GetUserTemp(2, 5, "2")
	// zkSocket.GetUserTemp(2, 6, "2")
	// zkSocket.GetUserTemp(2, 7, "2")
	// zkSocket.GetUserTemp(2, 8, "2")
	// zkSocket.GetUserTemp(2, 9, "2")
	// zkSocket.GetUserTemp(2, 0, "2")
	// nwU := gozk.User{
	// 	UID:       1324,
	// 	UserID:    "1324",
	// 	Name:      "Siwapong",
	// 	Privilege: 0,
	// 	Password:  "",
	// 	GroupID:   0,
	// 	Card:      "9876543",
	// }
	// err := zkSocket.SetUser(nwU)
	// if err != nil {
	// 	fmt.Println(err)
	// }

	// go func() {
	// 	for event := range c {
	// 		log.Println(event)
	// 	}
	// }()

	gracefulQuit(zkSocket.StopCapture)
}
func RepleTime(zs *gozk.ZK) {
	// zs.SetTime(time.Now())
	fmt.Println("RepleTime")
	cTime := time.Now()
	fmt.Println(cTime.Hour(), cTime.Minute())
	if cTime.Hour() == 16 && cTime.Minute() >= 58 {
		fmt.Println("Reset")
		dateString := "2023-05-04 16:58:00"
		//convert string to time.Time
		layout := "2006-01-02 15:04:05"
		t, _ := time.Parse(layout, dateString)
		err := zs.SetTime(t)
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println(err)
	}
	f := func() {
		RepleTime(zs)
	}
	time.AfterFunc(15*time.Second, f)
}
func gracefulQuit(f func()) {
	sigChan := make(chan os.Signal)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan

		log.Println("Stopping...")
		f()

		time.Sleep(time.Second * 1)
		os.Exit(1)
	}()

	for {
		time.Sleep(10 * time.Second) // or runtime.Gosched() or similar per @misterbee
	}
}
func myLogFunc(att *gozk.Attendance) {
	fmt.Println("MyDate", att.UserID, att.AttendedAt)
}
