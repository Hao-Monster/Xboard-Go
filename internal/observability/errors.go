package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"syscall"
)

var sqlStatePattern = regexp.MustCompile(`^[0-9A-Z]{5}$`)

// ErrorAttrs classifies only well-defined sentinel/typed error contracts. It
// never inspects Error() text, arbitrary error fields, addresses, SQL or URLs.
func ErrorAttrs(value any) []slog.Attr {
	attrs := []slog.Attr{slog.String("error_type", ErrorType(value))}
	class, code := "internal", "unknown"
	err, ok := value.(error)
	if !ok {
		return append(attrs, slog.String("error_class", class), slog.String("error_code", code))
	}
	switch {
	case errors.Is(err, context.Canceled):
		class, code = "context", "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		class, code = "context", "deadline_exceeded"
	case errors.Is(err, os.ErrNotExist):
		class, code = "filesystem", "not_exist"
	case errors.Is(err, os.ErrPermission):
		class, code = "filesystem", "permission_denied"
	case errors.Is(err, sql.ErrNoRows):
		class, code = "database", "no_rows"
	case errors.Is(err, sql.ErrTxDone):
		class, code = "database", "transaction_done"
	case errors.Is(err, net.ErrClosed):
		class, code = "network", "closed"
	default:
		var network *net.OpError
		var errno syscall.Errno
		var sqlState interface{ SQLState() string }
		var databaseCode interface{ Code() int }
		switch {
		case errors.As(err, &network):
			class, code = "network", "operation_failed"
			op := "other"
			switch network.Op {
			case "dial", "read", "write", "listen", "accept", "connect":
				op = network.Op
			}
			attrs = append(attrs, slog.String("network_op", op))
			if errors.As(err, &errno) {
				code = fmt.Sprintf("errno_%d", uint64(errno))
			}
		case errors.As(err, &errno):
			class, code = "system", fmt.Sprintf("errno_%d", uint64(errno))
		case errors.As(err, &sqlState):
			state := sqlState.SQLState()
			if sqlStatePattern.MatchString(state) {
				class, code = "database", "sqlstate_"+state
			}
		case errors.As(err, &databaseCode):
			n := databaseCode.Code()
			if n >= 0 && n <= 65535 {
				class, code = "database", fmt.Sprintf("code_%d", n)
			}
		}
	}
	return append(attrs, slog.String("error_class", class), slog.String("error_code", code))
}
