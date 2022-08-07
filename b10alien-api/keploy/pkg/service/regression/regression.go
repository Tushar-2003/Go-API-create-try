package regression

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.keploy.io/server/pkg"
	"go.keploy.io/server/pkg/models"
	"go.keploy.io/server/pkg/platform/telemetry"
	"go.keploy.io/server/pkg/service/run"
	"go.uber.org/zap"
)

func New(tdb models.TestCaseDB, rdb run.DB, log *zap.Logger, EnableDeDup bool, adb telemetry.Service, client http.Client) *Regression {
	return &Regression{
		tdb:         tdb,
		tele:        adb,
		log:         log,
		rdb:         rdb,
		client:      client,
		mu:          sync.Mutex{},
		anchors:     map[string][]map[string][]string{},
		noisyFields: map[string]map[string]bool{},
		fieldCounts: map[string]map[string]map[string]int{},
		EnableDeDup: EnableDeDup,
	}
}

type Regression struct {
	tdb      models.TestCaseDB
	tele     telemetry.Service
	rdb      run.DB
	client   http.Client
	log      *zap.Logger
	mu       sync.Mutex
	appCount int
	// index is `cid-appID-uri`
	//
	// anchors is map[index][]map[key][]value or map[index]combinationOfAnchors
	// anchors stores all the combinations of anchor fields for a particular index
	// anchor field is a low variance field which is used in the deduplication algorithm.
	// example: user-type or blood-group could be good anchor fields whereas timestamps
	// and usernames are bad anchor fields.
	// during deduplication only anchor fields are compared for new requests to determine whether its a duplicate or not.
	// other fields are ignored.
	anchors map[string][]map[string][]string
	// noisyFields is map[index][key]bool
	noisyFields map[string]map[string]bool
	// fieldCounts is map[index][key][value]count
	// fieldCounts stores the count of all values of a particular field in an index.
	// eg: lets say field is bloodGroup then the value would be {A+: 20, B+: 10,...}
	fieldCounts map[string]map[string]map[string]int
	EnableDeDup bool
}

func (r *Regression) DeleteTC(ctx context.Context, cid, id string) error {
	// reset cache
	r.mu.Lock()
	defer r.mu.Unlock()
	t, err := r.tdb.Get(ctx, cid, id)
	if err != nil {
		r.log.Error("failed to get testcases from the DB", zap.String("cid", cid), zap.Error(err))
		return errors.New("internal failure")
	}
	index := fmt.Sprintf("%s-%s-%s", t.CID, t.AppID, t.URI)
	delete(r.anchors, index)
	err = r.tdb.Delete(ctx, id)
	if err != nil {
		r.log.Error("failed to delete testcase from the DB", zap.String("cid", cid), zap.String("appID", t.AppID), zap.Error(err))
		return errors.New("internal failure")
	}

	r.tele.DeleteTc(r.client, ctx)
	return nil
}

func (r *Regression) GetApps(ctx context.Context, cid string) ([]string, error) {
	apps, err := r.tdb.GetApps(ctx, cid)
	if apps != nil && len(apps) != r.appCount {
		r.tele.GetApps(len(apps), r.client, ctx)
		r.appCount = len(apps)
	}
	return apps, err
}

// sanitiseInput sanitises user input strings before logging them for safety, removing newlines
// and escaping HTML tags. This is to prevent log injection, including forgery of log records.
// Reference: https://www.owasp.org/index.php/Log_Injection
func sanitiseInput(s string) string {
	re := regexp.MustCompile(`(\n|\n\r|\r\n|\r)`)
	return html.EscapeString(string(re.ReplaceAll([]byte(s), []byte(""))))
}

func (r *Regression) Get(ctx context.Context, cid, appID, id string) (models.TestCase, error) {
	tcs, err := r.tdb.Get(ctx, cid, id)
	if err != nil {
		sanitizedAppID := sanitiseInput(appID)
		r.log.Error("failed to get testcases from the DB", zap.String("cid", cid), zap.String("appID", sanitizedAppID), zap.Error(err))
		return models.TestCase{}, errors.New("internal failure")
	}
	return tcs, nil
}

func (r *Regression) GetAll(ctx context.Context, cid, appID string, offset *int, limit *int) ([]models.TestCase, error) {
	off, lim := 0, 25
	if offset != nil {
		off = *offset
	}
	if limit != nil {
		lim = *limit
	}

	tcs, err := r.tdb.GetAll(ctx, cid, appID, false, off, lim)

	if err != nil {
		sanitizedAppID := sanitiseInput(appID)
		r.log.Error("failed to get testcases from the DB", zap.String("cid", cid), zap.String("appID", sanitizedAppID), zap.Error(err))
		return nil, errors.New("internal failure")
	}
	return tcs, nil
}

func (r *Regression) UpdateTC(ctx context.Context, t []models.TestCase) error {
	for _, v := range t {
		err := r.tdb.UpdateTC(ctx, v)
		if err != nil {
			r.log.Error("failed to insert testcase into DB", zap.String("appID", v.AppID), zap.Error(err))
			return errors.New("internal failure")
		}
	}
	r.tele.EditTc(r.client, ctx)
	return nil
}

func (r *Regression) putTC(ctx context.Context, cid string, t models.TestCase) (string, error) {
	t.CID = cid

	var err error
	if r.EnableDeDup {
		// check if already exists
		dup, err := r.isDup(ctx, &t)
		if err != nil {
			r.log.Error("failed to run deduplication on the testcase", zap.String("cid", cid), zap.String("appID", t.AppID), zap.Error(err))
			return "", errors.New("internal failure")
		}
		if dup {
			r.log.Info("found duplicate testcase", zap.String("cid", cid), zap.String("appID", t.AppID), zap.String("uri", t.URI))
			return "", nil
		}
	}
	err = r.tdb.Upsert(ctx, t)
	if err != nil {
		r.log.Error("failed to insert testcase into DB", zap.String("cid", cid), zap.String("appID", t.AppID), zap.Error(err))
		return "", errors.New("internal failure")
	}

	return t.ID, nil
}

func (r *Regression) Put(ctx context.Context, cid string, tcs []models.TestCase) ([]string, error) {
	var ids []string
	if len(tcs) == 0 {
		return ids, errors.New("no testcase to update")
	}
	for _, t := range tcs {
		id, err := r.putTC(ctx, cid, t)
		if err != nil {
			msg := "failed saving testcase"
			r.log.Error(msg, zap.Error(err), zap.String("cid", cid), zap.String("id", t.ID), zap.String("app", t.AppID))
			return ids, errors.New(msg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (r *Regression) test(ctx context.Context, cid, id, app string, resp models.HttpResp) (bool, *run.Result, *models.TestCase, error) {

	tc, err := r.tdb.Get(ctx, cid, id)
	if err != nil {
		r.log.Error("failed to get testcase from DB", zap.String("id", id), zap.String("cid", cid), zap.String("appID", app), zap.Error(err))
		return false, nil, nil, err
	}
	bodyType := run.BodyTypePlain
	if json.Valid([]byte(resp.Body)) {
		bodyType = run.BodyTypeJSON
	}
	pass := true
	hRes := &[]run.HeaderResult{}
	res := &run.Result{
		StatusCode: run.IntResult{
			Normal:   false,
			Expected: tc.HttpResp.StatusCode,
			Actual:   resp.StatusCode,
		},
		BodyResult: run.BodyResult{
			Normal:   false,
			Type:     bodyType,
			Expected: tc.HttpResp.Body,
			Actual:   resp.Body,
		},
	}

	var (
		bodyNoise   []string
		headerNoise = map[string]string{}
	)

	for _, n := range tc.Noise {
		a := strings.Split(n, ".")
		if len(a) > 1 && a[0] == "body" {
			x := strings.Join(a[1:], ".")
			bodyNoise = append(bodyNoise, x)
		} else if a[0] == "header" {
			// if len(a) == 2 {
			// 	headerNoise[a[1]] = a[1]
			// 	continue
			// }
			headerNoise[a[len(a)-1]] = a[len(a)-1]
			// headerNoise[a[0]] = a[0]
		}
	}

	if !pkg.Contains(tc.Noise, "body") && bodyType == run.BodyTypeJSON {
		pass, err = pkg.Match(tc.HttpResp.Body, resp.Body, bodyNoise, r.log)
		if err != nil {
			return false, res, &tc, err
		}
	} else {
		if !pkg.Contains(tc.Noise, "body") && tc.HttpResp.Body != resp.Body {
			pass = false
		}
	}

	res.BodyResult.Normal = pass

	if !pkg.CompareHeaders(tc.HttpResp.Header, resp.Header, hRes, headerNoise) {
		pass = false
	}
	res.HeadersResult = *hRes

	if tc.HttpResp.StatusCode == resp.StatusCode {
		res.StatusCode.Normal = true
	} else {
		pass = false
	}

	return pass, res, &tc, nil
}

func (r *Regression) Test(ctx context.Context, cid, app, runID, id string, resp models.HttpResp) (bool, error) {
	var t *run.Test
	started := time.Now().UTC()
	ok, res, tc, err := r.test(ctx, cid, id, app, resp)
	if tc != nil {
		t = &run.Test{
			ID:         uuid.New().String(),
			Started:    started.Unix(),
			RunID:      runID,
			TestCaseID: id,
			URI:        tc.URI,
			Req:        tc.HttpReq,
			Dep:        tc.Deps,
			Resp:       resp,
			Result:     *res,
			Noise:      tc.Noise,
		}
	}
	t.Completed = time.Now().UTC().Unix()
	defer func() {
		err2 := r.saveResult(ctx, t)
		if err2 != nil {
			r.log.Error("failed test result to db", zap.Error(err2), zap.String("cid", cid), zap.String("app", app))
		}
	}()

	if err != nil {
		r.log.Error("failed to run the testcase", zap.Error(err), zap.String("cid", cid), zap.String("app", app))
		t.Status = run.TestStatusFailed
	}
	if ok {
		t.Status = run.TestStatusPassed
		return ok, nil
	}
	t.Status = run.TestStatusFailed
	return false, nil
}

func (r *Regression) saveResult(ctx context.Context, t *run.Test) error {
	err := r.rdb.PutTest(ctx, *t)
	if err != nil {
		return err
	}
	if t.Status == run.TestStatusFailed {
		err = r.rdb.Increment(ctx, false, true, t.RunID)
	} else {
		err = r.rdb.Increment(ctx, true, false, t.RunID)
	}

	if err != nil {
		return err
	}
	return nil
}

func (r *Regression) DeNoise(ctx context.Context, cid, id, app, body string, h http.Header) error {

	tc, err := r.tdb.Get(ctx, cid, id)
	if err != nil {
		r.log.Error("failed to get testcase from DB", zap.String("id", id), zap.String("cid", cid), zap.String("appID", app), zap.Error(err))
		return err
	}

	a, b := map[string][]string{}, map[string][]string{}

	// add headers
	for k, v := range tc.HttpResp.Header {
		a["header."+k] = []string{strings.Join(v, "")}
	}

	for k, v := range h {
		b["header."+k] = []string{strings.Join(v, "")}
	}

	err = addBody(tc.HttpResp.Body, a)
	if err != nil {
		r.log.Error("failed to parse response body", zap.String("id", id), zap.String("cid", cid), zap.String("appID", app), zap.Error(err))
		return err
	}

	err = addBody(body, b)
	if err != nil {
		r.log.Error("failed to parse response body", zap.String("id", id), zap.String("cid", cid), zap.String("appID", app), zap.Error(err))
		return err
	}
	// r.log.Debug("denoise between",zap.Any("stored object",a),zap.Any("coming object",b))
	var noise []string
	for k, v := range a {
		v2, ok := b[k]
		if !ok {
			noise = append(noise, k)
			continue
		}
		if !reflect.DeepEqual(v, v2) {
			noise = append(noise, k)
		}
	}
	// r.log.Debug("Noise Array : ",zap.Any("",noise))
	tc.Noise = noise
	err = r.tdb.Upsert(ctx, tc)
	if err != nil {
		r.log.Error("failed to update noise fields for testcase", zap.String("id", id), zap.String("cid", cid), zap.String("appID", app), zap.Error(err))
		return err
	}
	return nil
}

func addBody(body string, m map[string][]string) error {
	// add body
	if json.Valid([]byte(body)) {
		var result interface{}

		err := json.Unmarshal([]byte(body), &result)
		if err != nil {
			return err
		}
		j := flatten(result)
		for k, v := range j {
			nk := "body"
			if k != "" {
				nk = nk + "." + k
			}
			m[nk] = v
		}
	} else {
		// add it as raw text
		m["body"] = []string{body}
	}
	return nil
}

// Flatten takes a map and returns a new one where nested maps are replaced
// by dot-delimited keys.
// examples of valid jsons - https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/JSON/parse#examples
func flatten(j interface{}) map[string][]string {
	if j == nil {
		return map[string][]string{"": {""}}
	}
	o := make(map[string][]string)
	x := reflect.ValueOf(j)
	switch x.Kind() {
	case reflect.Map:
		m, ok := j.(map[string]interface{})
		if !ok {
			return map[string][]string{}
		}
		for k, v := range m {
			nm := flatten(v)
			for nk, nv := range nm {
				fk := k
				if nk != "" {
					fk = fk + "." + nk
				}
				o[fk] = nv
			}
		}
	case reflect.Bool:
		o[""] = []string{strconv.FormatBool(x.Bool())}
	case reflect.Float64:
		o[""] = []string{strconv.FormatFloat(x.Float(), 'E', -1, 64)}
	case reflect.String:
		o[""] = []string{x.String()}
	case reflect.Slice:
		child, ok := j.([]interface{})
		if !ok {
			return map[string][]string{}
		}
		for _, av := range child {
			nm := flatten(av)
			for nk, nv := range nm {
				if ov, exists := o[nk]; exists {
					o[nk] = append(ov, nv...)
				} else {
					o[nk] = nv
				}
			}
		}
	default:
		fmt.Println("found invalid value in json", j, x.Kind())
	}
	return o
}

func (r *Regression) fillCache(ctx context.Context, t *models.TestCase) (string, error) {

	index := fmt.Sprintf("%s-%s-%s", t.CID, t.AppID, t.URI)
	_, ok1 := r.noisyFields[index]
	_, ok2 := r.fieldCounts[index]
	if ok1 && ok2 {
		return index, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// check again after the lock
	_, ok1 = r.noisyFields[index]
	_, ok2 = r.fieldCounts[index]

	if !ok1 || !ok2 {
		var anchors []map[string][]string
		fieldCounts, noisyFields := map[string]map[string]int{}, map[string]bool{}
		tcs, err := r.tdb.GetKeys(ctx, t.CID, t.AppID, t.URI)
		if err != nil {
			return "", err
		}
		for _, v := range tcs {
			//var appAnchors map[string][]string
			//for _, a := range v.Anchors {
			//	appAnchors[a] = v.AllKeys[a]
			//}
			anchors = append(anchors, v.Anchors)
			for k, v1 := range v.AllKeys {
				if fieldCounts[k] == nil {
					fieldCounts[k] = map[string]int{}
				}
				for _, v2 := range v1 {
					fieldCounts[k][v2] = fieldCounts[k][v2] + 1
				}
				if !isAnchor(fieldCounts[k]) {
					noisyFields[k] = true
				}
			}
		}
		r.fieldCounts[index], r.noisyFields[index], r.anchors[index] = fieldCounts, noisyFields, anchors
	}
	return index, nil
}

func (r *Regression) isDup(ctx context.Context, t *models.TestCase) (bool, error) {

	reqKeys := map[string][]string{}
	filterKeys := map[string][]string{}

	index, err := r.fillCache(ctx, t)
	if err != nil {
		return false, err
	}

	// add headers
	for k, v := range t.HttpReq.Header {
		reqKeys["header."+k] = []string{strings.Join(v, "")}
	}

	// add url params
	for k, v := range t.HttpReq.URLParams {
		reqKeys["url_params."+k] = []string{v}
	}

	// add body if it is a valid json
	if json.Valid([]byte(t.HttpReq.Body)) {
		var result interface{}

		err = json.Unmarshal([]byte(t.HttpReq.Body), &result)
		if err != nil {
			return false, err
		}
		body := flatten(result)
		for k, v := range body {
			nk := "body"
			if k != "" {
				nk = nk + "." + k
			}
			reqKeys[nk] = v
		}
	}

	isAnchorChange := true
	for k, v := range reqKeys {
		if !r.noisyFields[index][k] {
			// update field count
			for _, s := range v {
				if _, ok := r.fieldCounts[index][k]; !ok {
					r.fieldCounts[index][k] = map[string]int{}
				}
				r.fieldCounts[index][k][s] = r.fieldCounts[index][k][s] + 1
			}
			if !isAnchor(r.fieldCounts[index][k]) {
				r.noisyFields[index][k] = true
				isAnchorChange = true
				continue
			}
			filterKeys[k] = v
		}
	}

	if len(filterKeys) == 0 {
		return true, nil
	}
	if isAnchorChange {
		err = r.tdb.DeleteByAnchor(ctx, t.CID, t.AppID, t.URI, filterKeys)
		if err != nil {
			return false, err
		}
	}

	// check if testcase based on anchor keys already exists
	dup, err := r.exists(ctx, filterKeys, index)
	if err != nil {
		return false, err
	}

	t.AllKeys = reqKeys
	//var keys []string
	//for k := range filterKeys {
	//	keys = append(keys, k)
	//}
	t.Anchors = filterKeys
	r.anchors[index] = append(r.anchors[index], filterKeys)

	return dup, nil
}

func (r *Regression) exists(_ context.Context, anchors map[string][]string, index string) (bool, error) {
	for _, v := range anchors {
		sort.Strings(v)
	}
	for _, v := range r.anchors[index] {
		if reflect.DeepEqual(v, anchors) {
			return true, nil
		}
	}
	return false, nil

}

func isAnchor(m map[string]int) bool {
	totalCount := 0
	for _, v := range m {
		totalCount = totalCount + v
	}
	// if total values for that field is less than 20 then,
	// the sample size is too small to know if its high variance.
	if totalCount < 20 {
		return true
	}
	// if the unique values are less than 40% of the total value count them,
	// the field is low variant.
	if float64(totalCount)*0.40 > float64(len(m)) {
		return true
	}
	return false
}
