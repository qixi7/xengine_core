package xlog

import (
	"github.com/qixi7/xengine_core/xlog/lumberjack-2.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var zapLogger *zap.Logger // logger

// new
func NewZapLogger(file string) {
	hook := lumberjack.Logger{
		Filename:   file, // 日志文件路径. 暂时就用一个文件即可, 先不分文件
		MaxSize:    64,   // 每个日志文件保存的最大尺寸 单位：M
		MaxBackups: 2,    // 日志文件最多保存多少个备份
		MaxAge:     14,   // 文件最多保存多少天
	}
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "@timestamp",
		LevelKey:       "loglevel",
		MessageKey:     "msg",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,  // 小写编码器
		EncodeTime:     zapcore.RFC3339TimeEncoder,     // RFC3339 UTC 时间格式
		EncodeDuration: zapcore.SecondsDurationEncoder, //
		EncodeName:     zapcore.FullNameEncoder,
	}
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),               // 编码器配置
		zapcore.NewMultiWriteSyncer(zapcore.AddSync(&hook)), // 打印到文件
		zap.NewAtomicLevel(),                                // 日志级别.默认INFO
	)

	zapLogger = zap.New(core)
}

// get
func GetZapLogger() *zap.Logger {
	return zapLogger
}

// sync
func ZapSync() error {
	if zapLogger != nil {
		zapLogger.Sync()
	}
	return nil
}
