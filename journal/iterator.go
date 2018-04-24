package journal

import (
	"context"
	"io"

	"github.com/kplr-io/container/btsbuf"
	"github.com/kplr-io/journal"
	"github.com/kplr-io/kplr/model"
	"github.com/kplr-io/kplr/model/evstrm"
)

type (
	// The Iterator allows to walk through a journal records forth or back.
	// It implements evstrm.Iterator interface what allows to use the iterator
	// in query pipelines.
	//
	// The iterator provides internal filtering records when ff function is provided
	Iterator struct {
		// A value which is used when returning IteratorPosition
		Id string

		// filter function is used for filtering records while iterating.
		FltF model.FilterF

		// the journal reader
		JReader journal.Reader

		// fwd defines whether ReadForward functions should be used or not
		fwd bool

		bbw btsbuf.Writer
		bbr btsbuf.Reader

		// curent position. Updated only when Get() is invoked
		pos journal.RecordId

		// The current LogEvent. The context is unsafe
		le    model.LogEvent
		valid bool
		err   error
	}

	IteratorPosition struct {
		Id       string
		Position journal.RecordId
	}
)

// NewIterator creates new iterator over the journal reader. It expects the reader
// buffer which will be used for reading next portion of records from the journal.
//
// The iterator receives the buf buffer and wraps it into extendable btsbuffer.Wrtier
// if the buffer is not big enough to read at least one record, journal will be able
// to extend it then.
func NewIterator(jr journal.Reader, buf []byte) *Iterator {
	it := new(Iterator)
	it.JReader = jr
	it.bbw.Reset(buf, true)
	it.fwd = true
	return it
}

// --------------------------- evstrm.Iterator -------------------------------
func (it *Iterator) End() bool {
	return !it.valid && it.Get(nil) != nil
}

func (it *Iterator) Get(le *model.LogEvent) error {
	if it.valid {
		if le != nil {
			*le = it.le
		}
		return nil
	}

	for {
		if !it.fillBuf() {
			return it.err
		}

		buf := it.bbr.Get()
		_, it.err = it.le.Unmarshal(buf)
		if it.err != nil {
			it.bbr.Reset(nil)
			return it.err
		}

		if it.FltF != nil && it.FltF(&it.le) {
			it.next()
			continue
		}

		if le != nil {
			*le = it.le
		}
		it.valid = true
		return nil
	}
}

func (it *Iterator) Next() {
	it.next()
	it.Get(nil)
}

func (it *Iterator) Backward(bkwrd bool) {
	if it.fwd != !bkwrd {
		it.dropBufToPos()
		it.fwd = !bkwrd
	}
}

func (it *Iterator) GetCurrentPos() journal.RecordId {
	return it.pos
}

func (it *Iterator) SetCurrentPos(pos journal.RecordId) {
	if it.pos != pos {
		it.pos = pos
		it.dropBufToPos()
	}
}

func (it *Iterator) WaitNewData(ctx context.Context) error {
	if !it.fwd {
		return it.err
	}

	it.dropBufToPos()
	if it.err != nil {
		return it.err
	}
	return it.JReader.WaitNewData(ctx)
}

func (it *Iterator) GetIteratorPos() evstrm.IteratorPos {
	return IteratorPosition{Id: it.Id, Position: it.pos}
}

func (it *Iterator) Close() error {
	err := it.JReader.Close()
	it.bbw.Close()
	it.bbr.Reset(nil)
	return err
}

func (it *Iterator) dropBufToPos() {
	it.err = nil
	it.valid = false
	it.bbr.Reset(nil)
	it.JReader.SetCurrentRecordId(it.pos)
}

// moves pos to forth or back ignoring filter condition
func (it *Iterator) next() {
	it.valid = false
	if !it.fillBuf() {
		return
	}

	if it.fwd {
		buf := it.bbr.Get()
		it.pos.Offset += int64(len(buf) + journal.DataRecMetaSize)
		it.bbr.Next()
	} else {
		it.bbr.Next()
		pos := it.pos
		if it.fillBuf() && it.pos == pos {
			buf := it.bbr.Get()
			it.pos.Offset -= int64(len(buf) + journal.DataRecMetaSize)
		}
	}
}

// fillBuf reads next portion of records. So as the le contains WeakString it's context
// is not trusted as soon, as the method is called
func (it *Iterator) fillBuf() bool {
	if it.err == io.EOF {
		it.err = nil
	}

	if !it.bbr.End() || it.err != nil {
		return it.err == nil
	}

	it.bbw.Reset(it.bbw.Buf(), true)
	if it.fwd {
		it.pos, it.err = it.JReader.ReadForward(&it.bbw)
	} else {
		it.pos, it.err = it.JReader.ReadBack(&it.bbw)
	}
	it.bbw.Close()

	if it.err != nil {
		it.bbr.Reset(nil)
		return false
	}
	it.err = it.bbr.Reset(it.bbw.Buf())
	return true
}
