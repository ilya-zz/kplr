package ingestor

import (
	"github.com/kplr-io/container/btsbuf"
	"github.com/kplr-io/geyser"
	"github.com/kplr-io/kplr/model"
)

// encoder structs intends to form a binary package will be send by Zebra
type encoder struct {
	bbw btsbuf.Writer
}

// newEncoder creates a new encoder object
func newEncoder() *encoder {
	e := new(encoder)
	e.bbw.Reset(make([]byte, 4096), true)
	return e
}

// encode forms a binary package for sending it through a wire. It expects header
// and a set of records in ev. As a result it returns slice of bytes or an error
// if any
func (e *encoder) encode(hdr *hdrsCacheRec, ev *geyser.Event) ([]byte, error) {
	e.bbw.Reset(e.bbw.Buf(), true)
	bf, err := e.bbw.Allocate(len(hdr.srcId), true)
	if err != nil {
		return nil, err
	}

	_, err = model.MarshalStringBuf(hdr.srcId, bf)
	if err != nil {
		return nil, err
	}

	first := true
	var le model.LogEvent
	for _, r := range ev.Records {
		if first {
			le.InitWithTagLine(int64(r.GetTs().UnixNano()), model.WeakString(r.Data), hdr.tags)
		} else {
			le.Init(int64(r.GetTs().UnixNano()), model.WeakString(r.Data))
		}
		first = false

		rb, err := e.bbw.Allocate(le.BufSize(), true)
		if err != nil {
			return nil, err
		}
		_, err = le.Marshal(rb)
		if err != nil {
			return nil, err
		}
	}

	return e.bbw.Close()
}
