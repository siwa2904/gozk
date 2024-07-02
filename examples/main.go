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
	zkSocket := gozk.NewZK(1, "192.168.1.201", 4370, 0, gozk.DefaultTimezone)
	if err := zkSocket.Connect(); err != nil {
		panic(err)
	}

	c := make(chan *gozk.Attendance, 50)
	// zkSocket.GetAttendances()
	// zkSocket.GetUserTemp(1, 2, "2")

	if err := zkSocket.LiveCapture(myLogFunc); err != nil {
		panic(err)
	}
	// zkSocket.GetTemplates()
	// go RepleTime(zkSocket)
	// zkSocket.DisableDevice()
	// zkSocket.GetTemplates()
	// zkSocket.EnableDevice()

	zkSocket.DisableDevice()

	attr, err := zkSocket.GetUsers()
	if err != nil {
		fmt.Println("Error", err.Error())
	}
	fmt.Println("LogTime::", attr)
	for _, attr := range attr {
		fmt.Println("Attr::", attr.UserID, attr.UserID, attr.Name, attr.Card)
	}
	zkSocket.EnableDevice()

	go func() {
		for event := range c {
			log.Println(event)
		}
	}()

	gracefulQuit(zkSocket.StopCapture)
}
func RepleTime(zs *gozk.ZK) {
	// zs.SetTime(time.Now())
	fmt.Println("RepleTime")
	cTime := time.Now()
	fmt.Println(cTime.Hour(), cTime.Minute())
	if cTime.Hour() == 16 && cTime.Minute() >= 58 {
		fmt.Println("Reset")
		//dateString := "2023-05-04 16:58:00"
		//convert string to time.Time
		//layout := "2006-01-02 15:04:05"
		//t, _ := time.Parse(layout, dateString)
		err := zs.SetTime(time.Now())
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
