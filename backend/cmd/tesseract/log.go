package main

import (
	"github.com/nightnoryu/go-kita/log"
	stdlog "log"
)

type serverLog struct{ logger log.Logger }

func (s serverLog) Write(p []byte) (int, error) {
	s.logger.WithFields(log.Fields{"error_code": "http_server_error"}).Warn("http_server_error")
	return len(p), nil
}
func stdLogger(logger log.Logger) *stdlog.Logger { return stdlog.New(serverLog{logger}, "", 0) }
