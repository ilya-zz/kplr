package api

import (
	"context"
	"time"

	am "github.com/kplr-io/api/model"
	"github.com/kplr-io/kplr/cursor"
	"github.com/kplr-io/kplr/model"
)

type logEventReader struct {
	ctx      context.Context
	cur      cursor.Cursor
	blocking bool
	cnt      int
	result   am.LogEvents
}

// newLogEventReader creates new log-events reader over the cursor. So as the
// readEvents() can block go-routine for a long time, ctx is provided. Invoker
// can interrupt the invocation by canceling the context. Cursor and number of
// records to be read as well. The blocking param controls the behavior in case
// of the end is reached, but requested number of records have not been read yet,
// if blocking is true, than the readEvents will be blocked until the number
// of records will be read - when EOF is met, it will wait for new data
func newLogEventReader(ctx context.Context, cur cursor.Cursor, limit int, blocking bool) *logEventReader {
	ler := new(logEventReader)
	ler.ctx = ctx
	ler.cur = cur
	ler.cnt = 0
	ler.result = make(am.LogEvents, limit)
	ler.blocking = blocking
	return ler
}

func (ler *logEventReader) onLogEvent(le *model.LogEvent) bool {
	if le == nil {
		return ler.blocking
	}

	ler.result[ler.cnt].Timestamp = time.Unix(0, le.GetTimestamp())
	ler.result[ler.cnt].Message = le.GetMessage().String()
	ler.cnt++
	return ler.cnt < cap(ler.result)
}

// readEvents walks trhough the journal and returns number of requested records
// it will return error if the read is failed by a reason. It will return non-nil
// error if the context is done as well.
func (ler *logEventReader) readEvents() (am.LogEvents, error) {
	err := ler.cur.Iterate(ler.ctx, ler.onLogEvent)
	if err != nil {
		return nil, err
	}
	return ler.result[:ler.cnt], nil
}
