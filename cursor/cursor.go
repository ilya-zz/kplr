package cursor

import (
	"context"
	"errors"
	"io"

	"github.com/jrivets/log4g"
	"github.com/kplr-io/kplr/journal"
	"github.com/kplr-io/kplr/model"
	"github.com/kplr-io/kplr/model/evstrm"
	"github.com/kplr-io/kplr/model/index"
	"github.com/kplr-io/kplr/model/kql"
	"github.com/kplr-io/kplr/mpool"
)

type (
	// CursorItFunc is a function which is used by the Cursor iterator to
	// notify the invoker by found events.
	CursorItFunc func(le *model.LogEvent) bool

	Cursor interface {
		io.Closer

		Id() string

		// TODO: replace GetReader onto Iterate. Reader will contain logic over
		// the iterator
		//
		// GetReader returns cursor read which reads up to limit number of records.
		// The exact param specifies the reader behavior when it reaches end of
		// the data, before it has read limit. If exact == true, then the reader
		// will block read process in case of it reaches EOF, but still has not
		// read limit lines. So it will try to reach limit by waiting new data
		// if it reaches EOF. If the exact==false, the reader will return EOF or
		// limit lines, which happens fist.
		//
		// limit < 0 means unlimited read
		GetReader(limit int64, exact bool) io.ReadCloser

		// Specifies filter for the cursor
		SetFilter(fltF model.FilterF)

		// SkipFromTail goes to tail and then skips the specific number of
		// records from there
		SkipFromTail(count int64)

		// Offset allows to skip recs records. If the recs is positive, then
		// it will skip records by the direction, if it is negative then it
		// will skip recs records in opposite direction
		Offset(recs int64)

		// Iterate walks through log events and notify invoker by calling cif
		// for every log event found. The function analyzes cif result and
		// continue, if it is positive, or stop the iteration if the cif returs false.
		//
		// The event iterator reaches EOF, it invokes cif with nil value for the
		// log event. If cif(nil) returns true, it means that the iterator should
		// wait for new records.
		//
		// Iterate receives context and it will stop processing when if finds
		// the context is closed.
		//
		// The function returns error if it is not io.EOF and further processing
		// is not possible.
		Iterate(ctx context.Context, cif CursorItFunc) error

		SetPosition(pos CursorPosition)
		GetPosition() CursorPosition
	}

	CursorSettings struct {
		CursorId  string
		Sources   []string
		Formatter *kql.Formatter
	}

	CursorProvider interface {
		// Executes the KQL query and returns Reader which will return
		NewCursor(cs *CursorSettings) (Cursor, error)
	}

	curProvider struct {
		JController journal.Controller `inject:""`
		MPool       mpool.Pool         `inject:"mPool"`
		TIndexer    index.TagsIndexer  `inject:"tIndexer"`
		logger      log4g.Logger
	}

	// cur is NOT thread-safe Cursor implementation
	cur struct {
		cp     *curProvider
		its    map[string]*journal.Iterator
		it     evstrm.Iterator
		logger log4g.Logger
		id     string
		le     model.LogEvent

		// fmtResult is used by the cursor to format every resulted line
		fmtResult curFmtF
	}
)

const (
	DEF_RD_BUF_SIZE = 16 * 1024
)

var (
	cUnkownIteratorPos = journal.IteratorPosition{}
)

func NewCursorProvider() CursorProvider {
	cp := new(curProvider)
	cp.logger = log4g.GetLogger("kplr.cursor.provider")
	return cp
}

// ============================ cur_provider =================================

// NewCursor creates new Cursor (cur instance). It expects list of sources - journal
// ids, where it will read data from.
func (cp *curProvider) NewCursor(cs *CursorSettings) (Cursor, error) {
	cp.logger.Debug("NewCursor with id=", cs.CursorId, " for ", cs.Sources)
	if len(cs.Sources) == 0 {
		return nil, errors.New("To create the cursor at least one of journal (data source) name should be specified")
	}

	var it evstrm.Iterator
	its := make(map[string]*journal.Iterator, len(cs.Sources))

	if len(cs.Sources) == 1 {
		rd, err := cp.JController.GetReader(cs.Sources[0])
		if err != nil {
			return nil, err
		}

		jit := journal.NewIterator(rd, cp.MPool.GetBtsBuf(DEF_RD_BUF_SIZE))
		jit.Id = cs.Sources[0]
		its[cs.Sources[0]] = jit

		it = jit
	} else {
		mxs := make([]evstrm.Iterator, len(cs.Sources))

		// first make basic journal iterators
		for i, srcId := range cs.Sources {
			rd, err := cp.JController.GetReader(srcId)
			if err != nil {
				return nil, err
			}

			jit := journal.NewIterator(rd, cp.MPool.GetBtsBuf(DEF_RD_BUF_SIZE))
			jit.Id = srcId
			its[srcId] = jit
			mxs[i] = jit
		}

		// mixing them now - building it
		for len(mxs) > 1 {
			for i := 0; i < len(mxs)-1; i += 2 {
				m := &evstrm.Mixer{}
				m.Reset(evstrm.GetEarliest, mxs[i], mxs[i+1])
				mxs[i/2] = m
			}
			if len(mxs)&1 == 1 {
				mxs[len(mxs)/2] = mxs[len(mxs)-1]
				mxs = mxs[:len(mxs)/2+1]
			} else {
				mxs = mxs[:len(mxs)/2]
			}
		}
		it = mxs[0]
	}

	c := new(cur)
	c.cp = cp
	c.it = it
	c.its = its
	c.id = cs.CursorId

	fmtr := newCurFromatter(c, cs.Formatter)
	c.fmtResult = fmtr.getCurFmtF()

	c.logger = log4g.GetLogger("kplr.cursor").WithId("{" + c.id + "}").(log4g.Logger)
	return c, nil
}

// ================================ cur ======================================
func (c *cur) Id() string {
	return c.id
}

// SetFilter specifies filter function which will be used when records from a
// journal are read
func (c *cur) SetFilter(fltF model.FilterF) {
	c.logger.Debug("SetFilter (fltF == nil?): ", fltF == nil)
	for _, ji := range c.its {
		ji.FltF = fltF
	}
}

func (c *cur) SetPosition(pos CursorPosition) {
	c.logger.Debug("SetPosition(): ", pos)
	if len(pos) == 1 {
		jp, ok := pos[""]
		if ok {
			// it is head or tail
			for _, it := range c.its {
				it.SetCurrentPos(jp)
			}
			return
		}
	}

	for srcId, recId := range pos {
		if it, ok := c.its[srcId]; ok {
			it.SetCurrentPos(recId)
		}
	}
}

func (c *cur) GetPosition() CursorPosition {
	res := make(CursorPosition)
	for j, jit := range c.its {
		res[j] = jit.GetCurrentPos()
	}
	return res
}

func (c *cur) SkipFromTail(count int64) {
	c.logger.Debug("Skipping ", count, " records from tail.")
	if count <= 0 || len(c.its) == 0 {
		return
	}
	c.it.Backward(true)
	c.SetPosition(CUR_POS_TAIL)

	itPos := cUnkownIteratorPos
	for ; !c.it.End() && count > 0; count-- {
		c.it.Get(nil)
		itPos = c.getCurIteratorPosition()
		if count > 1 {
			c.it.Next()
		}
	}

	c.it.Backward(false)
	c.meetPos(itPos)
}

func (c *cur) getCurIteratorPosition() journal.IteratorPosition {
	itp := c.it.GetIteratorPos()
	if itp != nil {
		return itp.(journal.IteratorPosition)
	}
	return cUnkownIteratorPos
}

// meetPos is used to find a proper point in mix of iterators when direction
// is changed. The method is needed in case of a mixer is involved. This case
// when direction is changed the cursor will not return the record it returned
// before the changing the direction. It is caused by using function to making
// the choice of the next record (mixer specific)
func (c *cur) meetPos(itPos journal.IteratorPosition) {
	if itPos == cUnkownIteratorPos || len(c.its) == 1 {
		return
	}

	for i := 0; i < len(c.its) && !c.it.End(); i++ {
		c.it.Get(nil)
		if c.getCurIteratorPosition() == itPos {
			return
		}
		c.it.Next()
	}
}

func (c *cur) Offset(count int64) {
	c.logger.Debug("Offset(): count=", count)
	if count >= 0 {
		for ; count > 0 && !c.it.End(); count-- {
			c.it.Get(nil)
			c.it.Next()
		}
		return
	}

	if c.it.End() {
		c.logger.Debug("We reached end, so call SkipFromTail()...")
		c.SkipFromTail(-count)
		return
	}

	itPos := c.getCurIteratorPosition()
	c.it.Backward(true)
	c.meetPos(itPos)

	for ; !c.it.End() && count < 0; count++ {
		c.it.Next()
		c.it.Get(nil)
		itPos = c.getCurIteratorPosition()
	}

	c.it.Backward(false)
	c.meetPos(itPos)
}

func (c *cur) GetReader(limit int64, exact bool) io.ReadCloser {
	return newCurReader(c, limit, exact)
}

// Close closes the cursor. All attempts to work with the cursor after it is closed
// are not allowed and can follow to unpredictable results
func (c *cur) Close() error {
	c.logger.Debug("Close().")
	err := c.it.Close()
	c.it = nil
	return err
}

func (c *cur) Iterate(ctx context.Context, cif CursorItFunc) (err error) {
	defer c.onReaderClosed()

	for ctx.Err() == nil {
		err = c.it.Get(&c.le)
		if err == nil {
			c.it.Next()
			if !cif(&c.le) {
				return nil
			}
			continue
		}

		if err == io.EOF {
			if !cif(nil) {
				return nil
			}
			c.waitRecords(ctx)
			continue
		}

		// an unrecoverable error
		return err
	}
	return ctx.Err()
}

// =============================== recsReader ================================

// nextRecord reads current log event, format it, if it was read successfully and
// iterate to next event. Returns the formatted string and an error if any
func (c *cur) nextRecord() ([]byte, error) {
	if c.it.End() {
		return nil, io.EOF
	}

	err := c.it.Get(&c.le)
	if err != nil {
		return nil, err
	}
	res := c.fmtResult()
	c.it.Next()

	return res, nil
}

// waitRecords blocks the current go routine until whether new data appears or the ctx
// context is closed
func (c *cur) waitRecords(ctx context.Context) {
	ctx2, cancel := context.WithCancel(ctx)
	for _, it := range c.its {
		go func(it *journal.Iterator) {
			it.WaitNewData(ctx2)
			cancel()
		}(it)
	}
	<-ctx2.Done()
}

// onReaderClosed releases all JReaders for iterators. This method suggested
// to be called every time when read operation is over or it is paused for a
// reason. By calling the method underlying readers are freed.
func (c *cur) onReaderClosed() {
	for _, it := range c.its {
		it.JReader.Close()
	}
}
