package api

import (
	"net/url"
	"testing"

	"github.com/kplr-io/api/model"
)

func TestApplyQueryParamsToKql(t *testing.T) {
	testApplyQueryParamsToKql(t, url.Values{"t": []string{"a"}}, "SELECT WHERE t=a")
	testApplyQueryParamsToKql(t, url.Values{"blocked": []string{"a"}}, "SELECT")
	testApplyQueryParamsToKql(t, url.Values{"where": []string{"a=b and c=d"}}, "SELECT WHERE a=b and c=d")
	testApplyQueryParamsToKql(t, url.Values{"where": []string{"a=b and c=d"}, "jj": []string{"hh"}}, "SELECT WHERE a=b and c=d AND jj=hh")
	testApplyQueryParamsToKql(t, url.Values{"format": []string{"a=b and c=d"}}, "SELECT \"a=b and c=d\"")
	testApplyQueryParamsToKql(t, url.Values{"from": []string{"a", "b"}}, "SELECT FROM \"a\",\"b\"")
}

func testApplyQueryParamsToKql(t *testing.T, q url.Values, expKql string) {
	var k model.Kql
	err := applyQueryParamsToKql(q, &k)
	if err != nil {
		t.Fatal("Ooops unexpected error while applying params err=", err)
	}
	if k.FormatKql() != expKql {
		t.Fatal("Received kql=", k.FormatKql(), ", but expected ", expKql)
	}
}
