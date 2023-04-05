package rdpkit

var loggerDebugF func(format string, v ...interface{})
var loggerInfoF func(format string, v ...interface{})
var loggerErrorF func(format string, v ...interface{})
var loggerWarnF func(format string, v ...interface{})

func debugF(format string, v ...interface{}) {
	if loggerDebugF != nil {
		loggerDebugF(format, v...)
	}
}

func infoF(format string, v ...interface{}) {
	if loggerInfoF != nil {
		loggerInfoF(format, v...)
	}
}

func warnF(format string, v ...interface{}) {
	if loggerWarnF != nil {
		loggerWarnF(format, v...)
	}
}

func errorF(format string, v ...interface{}) {
	if loggerErrorF != nil {
		loggerErrorF(format, v...)
	}
}

func SetDebugLogger(f func(format string, v ...interface{})) {
	loggerDebugF = f
}

func SetInfoLogger(f func(format string, v ...interface{})) {
	loggerInfoF = f
}

func SetErrorLogger(f func(format string, v ...interface{})) {
	loggerErrorF = f
}

func SetWarnLogger(f func(format string, v ...interface{})) {
	loggerWarnF = f
}
