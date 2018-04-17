package api

import (
	"github.com/kplr-io/api/model"
)

func toCurDescDO(cd *curDesc, curId string) *model.CurDesc {
	var curDO model.CurDesc
	curDO.Id = curId
	curDO.Created = model.ISO8601Time(cd.createdAt)
	curDO.LastTouched = model.ISO8601Time(cd.lastTouched)
	curDO.LastKQL = cd.lastKQL
	curDO.Position = curPosToCurPosDO(cd.cur.GetPosition())
	return &curDO
}
