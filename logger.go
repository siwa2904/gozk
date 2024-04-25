package gozk

import (
	"log"
)

type gozkLogger struct {
	stdLog *log.Logger
	errLog *log.Logger
}

func (zlog *gozkLogger) Info(v ...interface{}) {
	zlog.stdLog.Print(v...)
}
func (zlog *gozkLogger) Infof(format string, v ...interface{}) {
	zlog.stdLog.Printf(format, v...)
}
func (zlog *gozkLogger) Debug(v ...interface{}) {
	zlog.stdLog.Print(v...)
}
func (zlog *gozkLogger) Debugf(format string, v ...interface{}) {
	zlog.stdLog.Printf(format, v...)
}
func (zlog *gozkLogger) Error(v ...interface{}) {
	zlog.errLog.Println(v...)
}
func (zlog *gozkLogger) Errorf(format string, v ...interface{}) {
	zlog.errLog.Printf(format, v...)
}
