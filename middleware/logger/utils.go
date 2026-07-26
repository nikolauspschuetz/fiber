package logger

import (
	"io"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/internal/logtemplate"
	fiberlog "github.com/gofiber/fiber/v3/log"
	"github.com/gofiber/utils/v2"
)

func methodColor(method string, colors *fiber.Colors) string {
	if colors == nil {
		return ""
	}
	switch method {
	case fiber.MethodGet, fiber.MethodQuery:
		return colors.Cyan
	case fiber.MethodPost:
		return colors.Green
	case fiber.MethodPut:
		return colors.Yellow
	case fiber.MethodDelete:
		return colors.Red
	case fiber.MethodPatch:
		return colors.White
	case fiber.MethodHead:
		return colors.Magenta
	case fiber.MethodOptions:
		return colors.Blue
	default:
		return colors.Reset
	}
}

func statusColor(code int, colors *fiber.Colors) string {
	if colors == nil {
		return ""
	}
	switch {
	case code >= fiber.StatusOK && code < fiber.StatusMultipleChoices:
		return colors.Green
	case code >= fiber.StatusMultipleChoices && code < fiber.StatusBadRequest:
		return colors.Blue
	case code >= fiber.StatusBadRequest && code < fiber.StatusInternalServerError:
		return colors.Yellow
	default:
		return colors.Red
	}
}

type customLoggerWriter[T any] struct {
	loggerInstance fiberlog.AllLogger[T]
	level          fiberlog.Level
}

// Write implements io.Writer and forwards the payload to the configured logger.
func (cl *customLoggerWriter[T]) Write(p []byte) (int, error) {
	switch cl.level {
	case fiberlog.LevelTrace:
		cl.loggerInstance.Trace(utils.UnsafeString(p))
	case fiberlog.LevelDebug:
		cl.loggerInstance.Debug(utils.UnsafeString(p))
	case fiberlog.LevelInfo:
		cl.loggerInstance.Info(utils.UnsafeString(p))
	case fiberlog.LevelWarn:
		cl.loggerInstance.Warn(utils.UnsafeString(p))
	case fiberlog.LevelError:
		cl.loggerInstance.Error(utils.UnsafeString(p))
	default:
		return 0, nil
	}

	return len(p), nil
}

// LoggerToWriter is a helper function that returns an io.Writer that writes to a custom logger.
// You can integrate 3rd party loggers such as zerolog, logrus, etc. to logger middleware using this function.
//
// Valid levels: fiberlog.LevelInfo, fiberlog.LevelTrace, fiberlog.LevelWarn, fiberlog.LevelDebug, fiberlog.LevelError
func LoggerToWriter[T any](logger fiberlog.AllLogger[T], level fiberlog.Level) io.Writer {
	// Check if customLogger is nil
	if logger == nil {
		fiberlog.Panic("LoggerToWriter: customLogger must not be nil")
	}

	// Check if level is valid
	if level == fiberlog.LevelFatal || level == fiberlog.LevelPanic {
		fiberlog.Panic("LoggerToWriter: invalid level")
	}

	return &customLoggerWriter[T]{
		level:          level,
		loggerInstance: logger,
	}
}

// writeSanitized writes p to output with ASCII control bytes replaced by
// spaces (tabs are preserved), so user-controlled values such as a request
// body or a decoded query parameter cannot inject CR/LF and forge log lines.
func writeSanitized(output Buffer, p []byte) (int, error) {
	return logtemplate.WriteSanitized(output, p)
}

// writeSanitizedString is writeSanitized for strings.
func writeSanitizedString(output Buffer, s string) (int, error) {
	return logtemplate.WriteSanitizedString(output, s)
}

// writeSanitizedColored writes value between the two color escapes, scrubbing
// only value. The color sequences are library-controlled and must reach the
// output verbatim.
func writeSanitizedColored(output Buffer, color, value, reset string) (int, error) {
	n, err := output.WriteString(color)
	if err != nil {
		return n, err
	}
	m, err := writeSanitizedString(output, value)
	n += m
	if err != nil {
		return n, err
	}
	m, err = output.WriteString(reset)
	return n + m, err
}
