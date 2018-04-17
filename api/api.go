package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jrivets/log4g"
	"github.com/kplr-io/api/model"
	"github.com/kplr-io/container"
	"github.com/kplr-io/kplr"
	"github.com/kplr-io/kplr/cursor"
	"github.com/kplr-io/kplr/journal"
	"github.com/kplr-io/kplr/model/index"
	"github.com/kplr-io/kplr/model/kql"
	"github.com/teris-io/shortid"
)

type (
	RestApiConfig interface {
		GetHttpAddress() string
		GetHttpShtdwnTOSec() int
		IsHttpDebugMode() bool

		// GetHttpsCertFile in case of TLS returns non-empty filename with TLS cert
		GetHttpsCertFile() string
		// GetHttpsKeyFile in case of TLS returns non-empty filename with private key
		GetHttpsKeyFile() string
	}

	RestApi struct {
		logger         log4g.Logger
		ge             *gin.Engine
		srv            *http.Server
		TIndx          index.TagsIndexer     `inject:"tIndexer"`
		Config         RestApiConfig         `inject:"restApiConfig"`
		CursorProvider cursor.CursorProvider `inject:""`
		JrnlCtrlr      journal.Controller    `inject:""`
		MCtx           context.Context       `inject:"mainCtx"`

		rdsCnt    int32
		lock      sync.Mutex
		cursors   *container.Lru
		ctx       context.Context
		ctxCancel context.CancelFunc
	}

	apiError struct {
		err_tp int
		msg    string
	}

	error_resp struct {
		Status       int    `json:"status"`
		ErrorMessage string `json:"error"`
	}

	// cur_desc a structure which contains information about persisted cursors
	curDesc struct {
		cur cursor.Cursor

		createdAt   time.Time
		lastTouched time.Time
		lastKQL     string
	}
)

const (
	ERR_INVALID_CNT_TYPE = 1
	ERR_INVALID_PARAM    = 2
	ERR_NOT_FOUND        = 3

	CURSOR_TTL_SEC = 300
	MAX_CUR_SRCS   = 50
)

func NewError(tp int, msg string) error {
	return &apiError{tp, msg}
}

func (ae *apiError) Error() string {
	return ae.msg
}

func (ae *apiError) get_error_resp() error_resp {
	switch ae.err_tp {
	case ERR_INVALID_CNT_TYPE:
		return error_resp{http.StatusBadRequest, ae.msg}
	case ERR_INVALID_PARAM:
		return error_resp{http.StatusBadRequest, ae.msg}
	case ERR_NOT_FOUND:
		return error_resp{http.StatusNotFound, ae.msg}
	}
	return error_resp{http.StatusInternalServerError, ae.msg}
}

func NewRestApi() *RestApi {
	ra := new(RestApi)
	ra.logger = log4g.GetLogger("kplr.RestApi")
	ra.cursors = container.NewLru(math.MaxInt64, CURSOR_TTL_SEC*time.Second, ra.onCursorDeleted)
	return ra
}

func (ra *RestApi) DiPhase() int {
	return 1
}

func (ra *RestApi) DiInit() error {
	ra.logger.Info("Initializing. IsHttpDebugMode=", ra.Config.IsHttpDebugMode(), " listenOn=", ra.Config.GetHttpAddress(), ", shutdown timeout sec=", ra.Config.GetHttpShtdwnTOSec())
	if !ra.Config.IsHttpDebugMode() {
		gin.SetMode(gin.ReleaseMode)
	}

	ra.ge = gin.New()
	if ra.Config.IsHttpDebugMode() {
		ra.logger.Info("Gin logger and gin.debug is enabled. You can set up DEBUG mode for the ", ra.logger.GetName(), " group to obtain requests dumps and more logs for the API group.")
		log4g.SetLogLevel(ra.logger.GetName(), log4g.DEBUG)
		ra.ge.Use(gin.Logger())
	}

	// To log requests if DEBUG is enabled
	ra.ge.Use(ra.PrintRequest)
	// Recovery middleware recovers from any panics and writes a 500 if there was one.
	ra.ge.Use(gin.Recovery())

	ra.logger.Info("Constructing ReST API")

	// The ping returns pong and URI of the ping, how we see it.
	ra.ge.GET("/ping", ra.h_GET_ping)
	ra.ge.GET("/logs", ra.h_GET_logs)
	ra.ge.POST("/cursors", ra.h_POST_cursors)
	ra.ge.GET("/cursors/:curId", ra.h_GET_cursors_curId)
	ra.ge.GET("/cursors/:curId/logs", ra.h_GET_cursors_curId_logs)
	ra.ge.GET("/journals", ra.h_GET_journals)
	ra.ge.GET("/journals/:jId", ra.h_GET_journals_jId)

	ra.run()

	ra.ctx, ra.ctxCancel = context.WithCancel(context.Background())

	go func() {
		defer ra.logger.Info("Sweeper goroutine is over.")
	L1:
		for {
			select {
			case <-ra.ctx.Done():
				break L1
			case <-time.After((CURSOR_TTL_SEC / 2) * time.Second):
				ra.lock.Lock()
				ra.cursors.SweepByTime()
				ra.lock.Unlock()
			}
		}
	}()

	return nil
}

func (ra *RestApi) DiShutdown() {
	ra.ctxCancel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ra.Config.GetHttpShtdwnTOSec())*time.Second)
	defer cancel()

	if err := ra.srv.Shutdown(ctx); err != nil {
		ra.logger.Error("Server Shutdown err=", err)
	}
	ra.logger.Info("Shutdown.")
}

func (ra *RestApi) run() {
	ra.srv = &http.Server{
		Addr:    ra.Config.GetHttpAddress(),
		Handler: ra.ge,
	}

	go func() {
		ra.logger.Info("Running listener on ", ra.Config.GetHttpAddress())
		defer ra.logger.Info("Stopping listener")

		certFN := ra.Config.GetHttpsCertFile()
		keyFN := ra.Config.GetHttpsKeyFile()
		if certFN != "" || keyFN != "" {
			ra.logger.Info("Serves HTTPS connections: cert location ", certFN, ", private key at ", keyFN)
			if err := ra.srv.ListenAndServeTLS(certFN, keyFN); err != nil {
				ra.logger.Warn("Got the error from the server HTTPS listener err=", err)
			}
		} else {
			ra.logger.Info("Serves HTTP connections")
			if err := ra.srv.ListenAndServe(); err != nil {
				ra.logger.Warn("Got the error from the server HTTP listener err=", err)
			}
		}
	}()
}

// ============================== Filters ===================================
func (ra *RestApi) PrintRequest(c *gin.Context) {
	if ra.logger.GetLevel() >= log4g.DEBUG {
		r, _ := httputil.DumpRequest(c.Request, true)
		ra.logger.Debug("\n>>> REQUEST\n", string(r), "\n<<< REQUEST")
	}
	c.Next()
}

// ============================== Handlers ===================================
// GET /ping
func (ra *RestApi) h_GET_ping(c *gin.Context) {
	c.String(http.StatusOK, "pong URL conversion is "+composeURI(c.Request, ""))
}

// GET /logs
func (ra *RestApi) h_GET_logs(c *gin.Context) {
	q := c.Request.URL.Query()
	kqlTxt, err := ra.parseRequest(c, q, "tail")
	if ra.errorResponse(c, wrapErrorInvalidParam(err)) {
		ra.logger.Warn("GET /logs invalid reqeust err=", err)
		return
	}

	blocked := parseBoolean(q, "blocked", true)
	ra.logger.Debug("GET /logs kql=", kqlTxt, ", blocked=", blocked)

	cur, qry, err := ra.newCursorByQuery(kqlTxt)
	if ra.errorResponse(c, wrapErrorInvalidParam(err)) {
		return
	}

	rdr := cur.GetReader(qry.Limit(), blocked)
	ra.sendData(c, rdr, blocked)
	cur.Close()
}

// POST /cursors
func (ra *RestApi) h_POST_cursors(c *gin.Context) {
	curId, err := shortid.Generate()
	if ra.errorResponse(c, err) {
		return
	}

	q := c.Request.URL.Query()
	kqlTxt, err := ra.parseRequest(c, q, "head")
	if ra.errorResponse(c, wrapErrorInvalidParam(err)) {
		ra.logger.Warn("POST /cursors invalid reqeust err=", err)
		return
	}

	cur, _, err := ra.newCursorByQuery(kqlTxt)
	if ra.errorResponse(c, wrapErrorInvalidParam(err)) {
		return
	}
	cd := ra.newCurDesc(cur)
	cd.setKQL(kqlTxt)

	ra.logger.Info("New cursor desc curId=", curId, " ", cd)

	w := c.Writer
	uri := composeURI(c.Request, curId)
	w.Header().Set("Location", uri)
	ra.putCursorDesc(curId, cd)
	c.JSON(http.StatusCreated, toCurDescDO(cd, curId))
}

// GET /cursors/:curId
func (ra *RestApi) h_GET_cursors_curId(c *gin.Context) {
	curId := c.Param("curId")
	cd := ra.getCursorDesc(curId)
	if cd == nil {
		ra.errorResponse(c, NewError(ERR_NOT_FOUND, "The cursors id="+curId+" is not known"))
		return
	}

	defer ra.putCursorDesc(curId, cd)

	ra.logger.Debug("Get cursor state curId=", curId, " ", cd)
	c.JSON(http.StatusOK, toCurDescDO(cd, curId))
}

// GET /cursors/:curId/logs
func (ra *RestApi) h_GET_cursors_curId_logs(c *gin.Context) {
	curId := c.Param("curId")
	cd := ra.getCursorDesc(curId)
	if cd == nil {
		ra.errorResponse(c, NewError(ERR_NOT_FOUND, "The cursors id="+curId+" is not known"))
		return
	}

	defer ra.putCursorDesc(curId, cd)
	defer ra.logger.Info("Hello!")

	q := c.Request.URL.Query()

	limit := parseInt64(q, "limit", 1000)
	if limit < 1 || limit > 100000 {
		ra.errorResponse(c, NewError(ERR_INVALID_PARAM, fmt.Sprintf("Wrong limit value %d, expecting an integer in between 1..100000", limit)))
		return
	}

	offset := parseInt64(q, "offset", 0)
	blocked := parseBoolean(q, "blocked", false)
	pos := parseString(q, "position", "")

	err := ra.applyParamsToCursor(cd.cur, offset, pos)
	if ra.errorResponse(c, err) {
		return
	}

	ra.sendLogEvents(c, cd.cur, limit, blocked)
}

// GET /journals
func (ra *RestApi) h_GET_journals(c *gin.Context) {
	c.JSON(http.StatusOK, ra.JrnlCtrlr.GetJournals())
}

// GET /journals/:jId
func (ra *RestApi) h_GET_journals_jId(c *gin.Context) {
	jId := c.Param("jId")
	ji, err := ra.JrnlCtrlr.GetJournalInfo(jId)
	if ra.errorResponse(c, err) {
		return
	}
	c.JSON(http.StatusOK, ji)
}

func (ra *RestApi) newCurDesc(cur cursor.Cursor) *curDesc {
	cd := new(curDesc)
	cd.createdAt = time.Now()
	cd.cur = cur
	cd.lastTouched = cd.createdAt
	return cd
}

func (ra *RestApi) getCursorDesc(curId string) *curDesc {
	ra.lock.Lock()
	defer ra.lock.Unlock()

	val := ra.cursors.Peek(curId)
	if val == nil {
		return nil
	}
	cd := val.Val().(*curDesc)
	ra.cursors.DeleteNoCallback(curId)
	cd.lastTouched = time.Now()
	return cd
}

func (ra *RestApi) putCursorDesc(curId string, cd *curDesc) {
	ra.lock.Lock()
	ra.cursors.Put(curId, cd, 1)
	ra.lock.Unlock()
}

func (ra *RestApi) onCursorDeleted(k, v interface{}) {
	ra.logger.Info("Cursor ", k, " is deleted")
	cd := v.(*curDesc)
	cd.cur.Close()
}

// applyQueryParamsToKql receives query params in q and apply them to kql object.
// It is supposed that some part of query could be specified as query params.
// The following parameters can be specified:
// format - defines FORMAT part of KQL
// from - defines FROM part of KQL
// where - defines WHERE part of KQL
// position - defines POSITION part of KQL
// offset - defines OFFSET an integer value of KQL
// limit -  defines LIMIT an integer value of KQL
// blocked - skipped as not relevant for the KQL
// "other param" - any other value will be added to WHERE condition with AND
// 		concatenation
func applyQueryParamsToKql(q url.Values, kql *model.Kql) (err error) {
	var wb bytes.Buffer
	var iv int64
	for k, v := range q {
		if len(v) < 1 {
			continue
		}

		switch strings.ToLower(k) {
		case "format":
			kql.Format = model.GetStringPtr(v[0])
		case "from":
			if kql.From == nil {
				kql.From = make([]string, 0, len(v))
			}
			kql.From = append(kql.From, v...)
		case "where":
			if wb.Len() > 0 {
				wb.WriteString(" AND ")
			}
			wb.WriteString(v[0])
		case "position":
			kql.Position = model.GetStringPtr(v[0])
		case "offset":
			iv, err = strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				return NewError(ERR_INVALID_PARAM, fmt.Sprint("'offset' must be an integer, but got ", v[0]))
			}
			kql.Offset = model.GetInt64Ptr(iv)
		case "limit":
			iv, err = strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				return NewError(ERR_INVALID_PARAM, fmt.Sprint("'limit' must be an integer (negative value means no limit), but got ", v[0]))
			}
			kql.Limit = model.GetInt64Ptr(iv)
		case "blocked":
			// ignore blocked
			continue
		default:
			if wb.Len() > 0 {
				wb.WriteString(" AND ")
			}
			wb.WriteString(k)
			wb.WriteRune('=')
			wb.WriteString(v[0])
		}
	}
	kql.Where = model.GetStringPtr(wb.String())
	return nil
}

// parseRequest parses HTTP request. It expects query specified by url-encoded
// or via body of the request. It will try to parse the body, which's expected to be a
// JSON encoded model.Kql object, or a pure KQL text, first. In case of pure
// KQL is provided in the body, its params cannot be overwritten by the query
// params, so a error will be reported, if this happens. JSON model.Kql object's
// fields can be overwritten by the query params.
//
// the function returns KQL query or an error if any
func (ra *RestApi) parseRequest(c *gin.Context, q url.Values, defPos string) (string, error) {
	var kq model.Kql
	err := applyQueryParamsToKql(q, &kq)
	if err != nil {
		return "", err
	}
	if defPos != "" && kq.Position == nil {
		kq.Position = model.GetStringPtr(defPos)
	}

	var qbuf bytes.Buffer
	bdy := c.Request.Body
	if bdy != nil {
		n, _ := qbuf.ReadFrom(bdy)
		if n > 0 {
			if c.ContentType() == "application/json" {
				var kdao model.Kql
				err := json.Unmarshal(qbuf.Bytes(), &kdao)
				if err != nil {
					return "", NewError(ERR_INVALID_PARAM, fmt.Sprint("Could not unmarshal json dictated by content-type application/json:", err))
				}
				kdao.Apply(&kq)
				return kdao.FormatKql(), nil
			}

			if !kq.IsEmpty() {
				return "", NewError(ERR_INVALID_PARAM, fmt.Sprint("Invalid request - received pure KQL text in the body together with query params ",
					q, ". Only pure KQL text with query params is not allowed. You can provide the query in JSON format or query params only"))
			}

			return qbuf.String(), nil
		}
	}
	return kq.FormatKql(), nil
}

func (ra *RestApi) newCursorByQuery(kqlTxt string) (cursor.Cursor, *kql.Query, error) {
	qry, err := kql.Compile(kqlTxt, ra.TIndx)
	if err != nil {
		return nil, nil, NewError(ERR_INVALID_PARAM, fmt.Sprint("Parsing error of automatically generated query='", kqlTxt, "', check the query syntax (escape params needed?), parser says: ", err))
	}

	jrnls := qry.Sources()
	if len(jrnls) > MAX_CUR_SRCS {
		return nil, nil, NewError(ERR_INVALID_PARAM, fmt.Sprint("Number of sources for the log query execution exceds maximum allowed value ", MAX_CUR_SRCS, ", please make your query more specific"))
	}

	if len(jrnls) == 0 {
		return nil, nil, NewError(ERR_INVALID_PARAM, fmt.Sprint("Could not define sources for the query=", kqlTxt, ", the error=", err))
	}

	id := atomic.AddInt32(&ra.rdsCnt, 1)
	cur, err := ra.CursorProvider.NewCursor(&cursor.CursorSettings{
		CursorId:  strconv.Itoa(int(id)),
		Sources:   jrnls,
		Formatter: qry.GetFormatter(),
	})
	if err != nil {
		return cur, qry, err
	}

	// qry.Position() never returns ""
	posDO := qry.Position()
	pos, err := curPosDOToCurPos(posDO)
	if err != nil {
		return nil, nil, NewError(ERR_INVALID_PARAM, fmt.Sprint("Could not recognize position=", qry.Position(), " the error=", err))
	}

	cur.SetFilter(qry.GetFilterF())
	if posDO == "tail" {
		skip := int64(1)
		if qry.Offset() > 0 {
			skip = qry.Offset()
		}
		cur.SkipFromTail(skip)
	} else {
		cur.SetPosition(pos)
		cur.Offset(qry.Offset())
	}

	return cur, qry, nil
}

func (ra *RestApi) applyParamsToCursor(cur cursor.Cursor, offset int64, pos string) error {
	if len(pos) > 0 {
		ps, err := curPosDOToCurPos(pos)
		if err != nil {
			return NewError(ERR_INVALID_PARAM, fmt.Sprint("Could not recognize position=", pos, " the error=", err))
		}

		if pos == "tail" {
			skip := int64(1)
			if offset > 0 {
				skip = offset
			}
			cur.SkipFromTail(skip)
			offset = 0
		} else {
			cur.SetPosition(ps)
		}
	}
	cur.Offset(offset)
	return nil
}

// sendData copies data from the reader to the context writer. If blocked is
// true, that means reader can block Read operation and will not return io.EOF,
// this case we set up the connection notification to stop the copying process
// in case of the connection is closed to release resources properly.
func (ra *RestApi) sendData(c *gin.Context, rdr io.ReadCloser, blocked bool) {
	w := c.Writer
	id := atomic.AddInt32(&ra.rdsCnt, 1)

	ra.logger.Debug("sendData(): id=", id, ", blocked=", blocked)
	if blocked {
		notify := w.CloseNotify()
		clsd := make(chan bool)
		defer close(clsd)

		go func() {
		L1:
			for {
				select {
				case <-time.After(500 * time.Millisecond):
					w.Flush()
				case <-notify:
					ra.logger.Debug("sendData(): <-notify, id=", id)
					break L1
				case <-clsd:
					ra.logger.Debug("sendData(): <-clsd, id=", id)
					break L1
				case <-ra.MCtx.Done():
					ra.logger.Debug("sendData(): <-ra.MCtx.Done(), id=", id)
					break L1
				}
			}
			rdr.Close()
		}()
	}

	w.Header().Set("Content-Disposition", "attachment; filename=logs.txt")
	io.Copy(w, rdr)
	rdr.Close()
	ra.logger.Debug("sendData(): over id=", id)
}

func (ra *RestApi) sendLogEvents(c *gin.Context, cur cursor.Cursor, limit int64, blocked bool) {
	w := c.Writer
	id := atomic.AddInt32(&ra.rdsCnt, 1)

	ra.logger.Debug("sendLogEvents(): id=", id, ", limit=", limit, ", blocked=", blocked)
	ctx := ra.MCtx

	if blocked {
		notify := w.CloseNotify()
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)

		go func() {
		L1:
			for {
				select {
				case <-notify:
					ra.logger.Debug("sendLogEvents(): <-notify, id=", id)
					break L1
				case <-ctx.Done():
					ra.logger.Debug("sendLogEvents(): <-ctx.Done(), id=", id)
					break L1
				}
			}
			cancel()
		}()
	}

	ler := newLogEventReader(ctx, cur, int(limit), blocked)
	evs, err := ler.readEvents()
	if ra.errorResponse(c, err) {
		return
	}
	c.JSON(http.StatusOK, &model.LogEventsResponse{LogEvents: evs, NextPos: curPosToCurPosDO(cur.GetPosition())})
}

// =============================== cur_desc ==================================
func (cd *curDesc) setKQL(kql string) {
	cd.lastKQL = kql
	cd.lastTouched = time.Now()
}

func (cd *curDesc) String() string {
	return fmt.Sprint("{curId=", cd.cur.Id(), ", created=", cd.createdAt, ", lastTouched=", cd.lastTouched,
		", lastKql='", cd.lastKQL, "'}")
}

// ================================ Misc =====================================
func parseBoolean(q url.Values, paramName string, defVal bool) bool {
	val, ok := q[paramName]
	if !ok || len(val) < 1 {
		return defVal
	}

	res, err := strconv.ParseBool(val[0])
	if err != nil {
		return defVal
	}

	return res
}

func parseInt64(q url.Values, paramName string, defVal int64) int64 {
	val, ok := q[paramName]
	if !ok || len(val) < 1 {
		return defVal
	}

	res, err := strconv.ParseInt(val[0], 10, 64)
	if err != nil {
		return defVal
	}

	return res
}

func parseString(q url.Values, paramName string, defVal string) string {
	val, ok := q[paramName]
	if !ok || len(val) < 1 {
		return defVal
	}
	return val[0]
}

func bindAppJson(c *gin.Context, inf interface{}) error {
	return c.BindJSON(inf)
}

func reqOp(c *gin.Context) string {
	return fmt.Sprint(c.Request.Method, " ", c.Request.URL)
}

func wrapErrorInvalidParam(err error) error {
	if err == nil {
		return nil
	}
	return NewError(ERR_INVALID_PARAM, err.Error())
}

func (ra *RestApi) errorResponse(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}

	ae, ok := err.(*apiError)
	if ok {
		er := ae.get_error_resp()
		c.JSON(er.Status, &er)
		return true
	}

	if err == kplr.ErrNotFound {
		c.Status(http.StatusNotFound)
		return true
	}

	ra.logger.Warn("Bad request err=", err)
	c.JSON(http.StatusInternalServerError, &error_resp{http.StatusInternalServerError, err.Error()})
	return true
}

func composeURIWithPath(r *http.Request, pth, id string) string {
	return resolveScheme(r) + "://" + path.Join(resolveHost(r), pth, id)
}

func composeURI(r *http.Request, id string) string {
	var resURL string
	if r.URL.IsAbs() {
		resURL = path.Join(r.URL.String(), id)
	} else {
		resURL = resolveScheme(r) + "://" + path.Join(resolveHost(r), r.URL.String(), id)
	}
	return resURL
}

func resolveScheme(r *http.Request) string {
	switch {
	case r.Header.Get("X-Forwarded-Proto") == "https":
		return "https"
	case r.URL.Scheme == "https":
		return "https"
	case r.TLS != nil:
		return "https"
	case strings.HasPrefix(r.Proto, "HTTPS"):
		return "https"
	default:
		return "http"
	}
}

func resolveHost(r *http.Request) (host string) {
	switch {
	case r.Header.Get("X-Forwarded-For") != "":
		return r.Header.Get("X-Forwarded-For")
	case r.Header.Get("X-Host") != "":
		return r.Header.Get("X-Host")
	case r.Host != "":
		return r.Host
	case r.URL.Host != "":
		return r.URL.Host
	default:
		return ""
	}
}
