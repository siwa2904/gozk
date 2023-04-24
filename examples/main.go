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
	zkSocket := gozk.NewZK(0, "192.168.1.20", 4370, 0, gozk.DefaultTimezone)
	if err := zkSocket.Connect(); err != nil {
		panic(err)
	}

	// c := make(chan *gozk.Attendance, 50)

	if err := zkSocket.LiveCapture(myLogFunc); err != nil {
		panic(err)
	}

	nwU := gozk.User{
		UID:       1324,
		UserID:    "1324",
		Name:      "Siwapong",
		Privilege: 0,
		Password:  "",
		GroupID:   0,
		Card:      "9876543",
	}
	err := zkSocket.SetUser(nwU)
	if err != nil {
		fmt.Println(err)
	}

	// go func() {
	// 	for event := range c {
	// 		log.Println(event)
	// 	}
	// }()

	gracefulQuit(zkSocket.StopCapture)
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
