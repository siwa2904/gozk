package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chenall/gozk"
)

func main() {
	zkSocket := gozk.NewZK(0, "192.168.0.201", 4370, 0, gozk.DefaultTimezone)
	if err := zkSocket.Connect(); err != nil {
		panic(err)
	}

	c := make(chan *gozk.Attendance, 50)

	if err := zkSocket.LiveCapture(c); err != nil {
		panic(err)
	}

	go func() {
		for event := range c {
			log.Println(event)
		}
	}()

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
