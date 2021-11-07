package xlog

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	Dir           string
	LogLevel      = offLog
	std           *log.Logger
	stdWriter     io.WriteCloser
	logToConsole  = true
	flushInterval = 1
)

const (
	offLog   int = iota // 0
	debugLog            // 1
	infoLog             // 2
	warnLog             // 3
	errorLog            // 4
	fatalLog            // 5

	stdTimeFormat = "2006-01-02T15:04:05.99999Z07:00 " // RFC3339Nano
)

var logName = []string{
	debugLog: "[DEBUG] ",
	infoLog:  "[INFO] ",
	warnLog:  "[WARN] ",
	errorLog: "[ERROR] ",
	fatalLog: "[FATAL] ",
}

func preParseArg(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		s := args[i]
		if strings.Contains(s, "-"+name) {
			sli := strings.FieldsFunc(s, func(r rune) bool {
				return string(r) == "="
			})
			if len(sli) <= 1 {
				return ""
			}
			return sli[1]
		}
	}

	return ""
}

func init() {
	var fname string
	flag.StringVar(&Dir, "log.dir", "./logs", "std log path")
	flag.StringVar(&fname, "log.name", "null", "log file name")
	flag.BoolVar(&logToConsole, "log.console", true, "log to console")
	flag.IntVar(&LogLevel, "log.level", 0, "log level")
	flag.IntVar(&flushInterval, "log.flush", 1, "log flush interval(in seconds)")

	exeName := preParseArg(os.Args[1:], "log.name")
	if len(exeName) == 0 {
		exeName = filepath.Base(os.Args[0])
	}
	exeName += ".log"

	Dir = preParseArg(os.Args[1:], "log.dir")
	if len(Dir) == 0 {
		Dir = "./logs"
	}

	fname = Dir + "/" + exeName

	var existErr error
	if Dir != "." && os.IsNotExist(existErr) {
		if existErr != nil {
			panic(fmt.Sprintf("make logs dir %s, existErr=%v", Dir, existErr.Error()))
		}
		if err := os.Mkdir(Dir, 0744); err != nil {
			panic(fmt.Sprintf("make logs dir %s : %s", Dir, err.Error()))
		}
	}
	stdWriter = NewLogger(fname, 100, 30, 200, 1024, 1)
	std = log.New(stdWriter, "", log.Lshortfile)
	log.SetFlags(log.Lshortfile)
}

func output(lvl int, skip int, format string, v ...interface{}) {
	preFix := logName[lvl]
	var str string
	if format == "" {
		str = fmt.Sprint(v...)
	} else {
		str = fmt.Sprintf(format, v...)
	}

	preFix += time.Now().Format(stdTimeFormat)
	if std != nil {
		std.SetPrefix(preFix)
		std.Output(skip+3, str)
	}
	if logToConsole {
		log.SetPrefix(preFix)
		log.Output(skip+3, str)
	}
}

func Debug(v ...interface{}) {
	if LogLevel > debugLog {
		return
	}
	output(debugLog, 0, "", v...)
}

func Debugf(format string, v ...interface{}) {
	if LogLevel > debugLog {
		return
	}
	output(debugLog, 0, format, v...)
}

func Info(v ...interface{}) {
	if LogLevel > infoLog {
		return
	}
	output(infoLog, 0, "", v...)
}

func InfoF(format string, v ...interface{}) {
	if LogLevel > infoLog {
		return
	}
	output(infoLog, 0, format, v...)
}

func Warn(v ...interface{}) {
	if LogLevel > warnLog {
		return
	}
	output(warnLog, 0, "", v...)
}

func Warnf(format string, v ...interface{}) {
	if LogLevel > warnLog {
		return
	}
	output(warnLog, 0, format, v...)
}

func Error(v ...interface{}) {
	if LogLevel > errorLog {
		return
	}
	output(errorLog, 0, "", v...)
}

func Errorf(format string, v ...interface{}) {
	if LogLevel > errorLog {
		return
	}
	output(errorLog, 0, format, v...)
}

func ErrorfSkip(skip int, format string, v ...interface{}) {
	if LogLevel > errorLog {
		return
	}
	output(errorLog, skip, format, v...)
}

func DebugfSkip(skip int, format string, v ...interface{}) {
	if LogLevel > errorLog {
		return
	}
	output(debugLog, skip, format, v...)
}

func InfofSkip(skip int, format string, v ...interface{}) {
	if LogLevel > errorLog {
		return
	}
	output(infoLog, skip, format, v...)
}

func Fatal(v ...interface{}) {
	if LogLevel > fatalLog {
		return
	}
	output(fatalLog, 0, "", v...)
}

func Fatalf(format string, v ...interface{}) {
	if LogLevel > fatalLog {
		return
	}
	output(fatalLog, 0, format, v...)
}

func Sync() error {
	if reallogger, ok := stdWriter.(*Logger); ok {
		reallogger.Sync()
	}
	return nil
}

func Close() {
	if std != nil {
		if reallogger, ok := stdWriter.(*Logger); ok {
			reallogger.Sync()
		}
		stdWriter.Close()
		stdWriter = nil
		std = nil
	}
	return
}
