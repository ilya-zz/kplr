package model

import (
	"reflect"
	"testing"
)

func TestTLNewTags(t *testing.T) {
	tl := TagLine("")
	tags, err := tl.NewTags(123)
	if err != nil || tags.gId != 123 || tags.tl != tl || len(tags.tm) != 0 {
		t.Fatal("Expecting ok, but err=", err)
	}

	tl = TagLine("wrongvalue")
	tags, err = tl.NewTags(123)
	if err == nil {
		t.Fatal("Expecting wrong value, but tags=", tags)
	}

	tl = TagLine("k=value")
	tags, err = tl.NewTags(12)
	if err != nil || tags.gId != 12 || tags.tl != tl || len(tags.tm) != 1 || tags.tm["k"] != "value" {
		t.Fatal("Expecting ok, but err=", err, " ", tags)
	}
}

func TestTMNewTagMap(t *testing.T) {
	tm := TagMap{}
	tags, err := tm.NewTags(123)
	if tags.gId != 123 || tags.tl != "" || len(tags.tm) != 0 {
		t.Fatal("Expecting ok, but err=", err, " tags=", tags)
	}

	tm = TagMap{"c": "aaa", "a": "cccc"}
	tags, err = tm.NewTags(34)
	if tags.gId != 34 || tags.tl != TagLine("a=cccc|c=aaa") || !reflect.DeepEqual(tags.tm, tm) {
		t.Fatal("Expecting ok, but err=", err, " tags=", tags)
	}
}

func TestMarshalUnmarshalTags(t *testing.T) {
	tl := TagLine("k=value|k1=value2")
	tags, err := tl.NewTags(123)
	if err != nil {
		t.Fatal("could not create tags err=", err)
	}

	if len(tags.tm) != 2 {
		t.Fatal("unexpected tags=", tags)
	}

	res, err := tags.MarshalJSON()
	if err != nil {
		t.Fatal("could not marshal err=", err)
	}

	var tags2 Tags
	err = tags2.UnmarshalJSON(res)
	if err != nil {
		t.Fatal("could not unmarshal err=", err)
	}

	if !reflect.DeepEqual(tags, tags2) {
		t.Fatal("Expected ", tags, ", but got ", tags2)
	}
}
