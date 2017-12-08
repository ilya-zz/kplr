package model

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func marshalStringByCast(v string, buf []byte) (int, error) {
	bl := len(buf)
	ln := len(v)
	if ln+4 > bl {
		return 0, noBufErr("MarshalString-size-body", bl, ln+4)
	}
	binary.BigEndian.PutUint32(buf, uint32(ln))
	copy(buf[4:ln+4], []byte(v))
	return ln + 4, nil
}

var tstStr = "This is some string for test marshalling speed Yahhoooo 11111111111111111111111111111111111111111111111111"
var tstTags = "pod=1234134kjhakfdjhlakjdsfhkjahdlf,key=abc"

func BenchmarkMarshalStringByCast(b *testing.B) {
	var buf [200]byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		marshalStringByCast(tstStr, buf[:])
	}
}

func BenchmarkMarshalString(b *testing.B) {
	var buf [200]byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MarshalString(tstStr, buf[:])
	}
}

func BenchmarkUnmarshalStringByCast(b *testing.B) {
	var buf [200]byte
	marshalStringByCast(tstStr, buf[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		UnmarshalStringCopy(buf[:])
	}
}

func BenchmarkUnmarshalString(b *testing.B) {
	var buf [200]byte
	marshalStringByCast(tstStr, buf[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		UnmarshalString(buf[:])
	}
}

func BenchmarkMarshalEvent(b *testing.B) {
	meta := []FieldType{FTInt64, FTString, FTInt64}
	ev := Event([]interface{}{int64(1234898734), tstStr, int64(97495739)})
	var store [200]byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MarshalEvent(meta, ev, store[:])
	}
}

func BenchmarkUnmarshalEventFast(b *testing.B) {
	meta := []FieldType{FTInt64, FTString, FTInt64}
	ev := Event([]interface{}{int64(1234898734), tstStr, int64(97495739)})
	var store [200]byte
	MarshalEvent(meta, ev, store[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		UnmarshalEvent(meta, store[:], ev)
	}
}

func BenchmarkUnmarshalEventByCopyStrings(b *testing.B) {
	meta := []FieldType{FTInt64, FTString, FTInt64}
	ev := Event([]interface{}{int64(1234898734), tstStr, int64(97495739)})
	var store [200]byte
	MarshalEvent(meta, ev, store[:])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		UnmarshalEventCopy(meta, store[:], ev)
	}
}

func TestGeneral(t *testing.T) {
	meta := []FieldType{FTInt64, FTString, FTInt64}
	ev := Event([]interface{}{int64(1234898734), "Hello World", int64(97495739)})
	var store [100]byte
	n, err := MarshalEvent(meta, ev, store[:])
	if n < 20 || err != nil {
		t.Fatal("Something goes wrong, should be marshal ok err=", err)
	}
	if n != ev.Size(meta) {
		t.Fatal("Oops, the marshaled size ", n, " and desired Size()=", ev.Size(meta), " are different!")
	}
	ev2 := Event(make([]interface{}, len(meta)))
	n2, err := UnmarshalEvent(meta, store[:], ev2)
	if n2 != n || err != nil {
		t.Fatal("Something goes wrong, should be umarshal ok err=", err, ", n=", n, ", n2=", n2)
	}
	if !reflect.DeepEqual(ev, ev2) {
		t.Fatal("ev=", ev, ", must be equal to ev2=", ev2)
	}

	ev[0] = nil
	ev[1] = nil
	n3, err := MarshalEvent(meta, ev, store[:])
	if n3 >= n || err != nil {
		t.Fatal("Something goes wrong, should be marshal ok err=", err, ", n3=", n3, ", n=", n)
	}
	if n3 != ev.Size(meta) {
		t.Fatal("Oops, the marshaled size ", n3, " and desired Size()=", ev.Size(meta), " are different!")
	}
	n2, err = UnmarshalEvent(meta, store[:], ev2)
	if n3 != n2 || err != nil {
		t.Fatal("Something goes wrong, should be umarshal ok err=", err, ", n=", n, ", n2=", n2)
	}
	if !reflect.DeepEqual(ev, ev2) {
		t.Fatal("ev=", ev, ", must be equal to ev2=", ev2)
	}
}

func TestMarshalString(t *testing.T) {
	str := "hello str"
	buf := make([]byte, len(str)+4)
	n, err := MarshalString(str, buf)
	if err != nil {
		t.Fatal("Should be enough space, but err=", err)
	}
	if n != len(str)+4 {
		t.Fatal("expected string marshal size is ", len(str)+4, ", but actual is ", n)
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	str := "hello str"
	buf := make([]byte, len(str)+4)
	MarshalString(str, buf)
	_, str2, _ := UnmarshalString(buf)
	if str2 != str {
		t.Fatal("Wrong unmarshaling str=", str, ", str2=", str2)
	}

	buf[4] = buf[5]
	if str2 == str {
		t.Fatal("They must be different now: str=", str, ", str2=", str2)
	}
}
