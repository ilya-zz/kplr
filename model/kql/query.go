package kql

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kplr-io/kplr"
	"github.com/kplr-io/kplr/model"
	"github.com/kplr-io/kplr/model/index"
)

type (
	// tagGroupDesc struct is a helper, which is used for caching tags check results
	tagGroupDesc struct {
		// chkRes contains result of tgChkFunc to the tags group
		chkRes bool
		// tags contains the tags for the group
		tags *model.Tags
	}

	Query struct {
		QSel    *Select
		idxer   index.TagsIndexer
		tgCache map[int64]*tagGroupDesc

		// tags check pair - valuator and function
		tgChkValuator queryExpValuator
		tgChkFunc     *expFuncDesc

		// records valuation
		recChkValuator queryExpValuator
		recsChkFunc    *expFuncDesc

		// source journals
		jrnls []string

		// result format
		formatter *Formatter
	}

	queryExpValuator struct {
		le   *model.LogEvent
		tags *model.Tags
	}
)

func Compile(kQuery string, idxer index.TagsIndexer) (*Query, error) {
	s, err := Parse(kQuery)
	if err != nil {
		return nil, err
	}

	fmtr, err := NewFormatter(kplr.GetStringVal(s.Format, "{msg}"), nil)
	if err != nil {
		return nil, err
	}

	qry := new(Query)
	qry.QSel = s
	qry.formatter = fmtr
	qry.idxer = idxer
	qry.tgCache = make(map[int64]*tagGroupDesc)
	qry.tgChkFunc, err = evaluate(s.Where, &qry.tgChkValuator, cExpValMsgIgnore|cExpValTsIgnore)
	if err != nil {
		return nil, err
	}

	qry.recsChkFunc, err = evaluate(s.Where, &qry.recChkValuator, 0)
	if err != nil {
		return nil, err
	}

	var jrnls []string
	if qry.tgChkFunc.typ == cFTIgnore {
		jrnls = idxer.GetAllJournals()
	} else {
		m := make(map[string]bool)
		idxer.Visit(func(td *index.TagsDesc) bool {
			qry.tgChkValuator.tags = td.Tags
			tgd := &tagGroupDesc{qry.tgChkFunc.fn(), td.Tags}
			qry.tgCache[td.Tags.GetId()] = tgd
			if tgd.chkRes {
				for j := range td.Journals {
					m[j] = true
				}
			}
			return true
		})

		jrnls = make([]string, 0, len(m))
		for j := range m {
			jrnls = append(qry.jrnls, j)
		}
	}
	qry.jrnls, err = filterJournals(jrnls, buildFromList(s.From))
	return qry, err
}

func (q *Query) String() string {
	return fmt.Sprintf("{jrnls: %v, select: %v}", q.jrnls, q.QSel)
}

func (q *Query) Limit() int64 {
	return int64(q.QSel.Limit)
}

func (q *Query) Offset() int64 {
	return kplr.GetInt64Val(q.QSel.Offset, 0)
}

func (q *Query) Sources() []string {
	return q.jrnls
}

func (q *Query) GetFormatter() *Formatter {
	return q.formatter
}

// Position returns position provided, or head, if the position was skipped in
// the original query
func (q *Query) Position() string {
	if q.QSel.Position == nil {
		return "head"
	}
	return q.QSel.Position.PosId
}

func (q *Query) GetFilterF() model.FilterF {
	return q.Filter
}

// Filter returns true if the log event le must be filtered
func (q *Query) Filter(le *model.LogEvent) bool {
	gid := le.GetTGroupId()

	tgd, ok := q.tgCache[gid]
	if !ok {
		td := q.idxer.GetTagsDesc(gid)
		if td != nil {
			q.tgChkValuator.tags = td.Tags
			tgd = &tagGroupDesc{q.tgChkFunc.fn(), td.Tags}
			q.tgCache[gid] = tgd
		}
	}

	if tgd != nil {
		if q.tgChkFunc.typ != cFTIgnore && !tgd.chkRes {
			// must be filtered cause the sub-expression depends on tags, which is false
			return true
		}
		q.recChkValuator.tags = tgd.tags
	} else {
		q.recChkValuator.tags = nil
	}

	q.recChkValuator.le = le
	return !q.recsChkFunc.fn()
}

// tagGroupDesc provide expValuator to check tags
func (qv *queryExpValuator) timestamp() int64 {
	return qv.le.GetTimestamp()
}

func (qv *queryExpValuator) msg() model.WeakString {
	return qv.le.GetMessage()
}

func (qv *queryExpValuator) tagVal(tag string) string {
	if qv.tags == nil {
		return ""
	}
	return qv.tags.GetValue(tag)
}

// filterJournals returns list of journals filtered by the fltExpr.
//
// NOTE!!! The function will change initial jrnls array.
func filterJournals(jrnls, exprs []string) ([]string, error) {
	if len(exprs) == 0 || (len(exprs) == 1 && exprs[0] == "") {
		return jrnls, nil
	}

	res := make([]string, 0, len(jrnls))

	// patterns
	for _, expr := range exprs {
		if len(jrnls) == 0 {
			break
		}

		ptrn := unquote(expr)
		ptrn = strings.Trim(ptrn, " ")

		for i := 0; i < len(jrnls); {
			j := jrnls[i]
			if ptrn != "*" {
				m, err := filepath.Match(ptrn, j)
				if err != nil {
					return res, err
				}
				if !m {
					i++
					continue
				}
			}

			res = append(res, j)
			jrnls[i] = jrnls[len(jrnls)-1]
			jrnls = jrnls[:len(jrnls)-1]
		}
	}

	return res, nil
}

func buildFromList(jl *NamesList) []string {
	if jl == nil || len(jl.JrnlNames) == 0 {
		return []string{}
	}

	res := make([]string, 0, len(jl.JrnlNames))
	for _, jn := range jl.JrnlNames {
		if jn != nil {
			res = append(res, jn.Name)
		}
	}

	return res
}
