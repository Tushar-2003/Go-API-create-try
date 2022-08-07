package models

import "context"

type TestCase struct {
	ID       string              `json:"id" bson:"_id"`
	Created  int64               `json:"created" bson:"created,omitempty"`
	Updated  int64               `json:"updated" bson:"updated,omitempty"`
	Captured int64               `json:"captured" bson:"captured,omitempty"`
	CID      string              `json:"cid" bson:"cid,omitempty"`
	AppID    string              `json:"app_id" bson:"app_id,omitempty"`
	URI      string              `json:"uri" bson:"uri,omitempty"`
	HttpReq  HttpReq             `json:"http_req" bson:"http_req,omitempty"`
	HttpResp HttpResp            `json:"http_resp" bson:"http_resp,omitempty"`
	Deps     []Dependency        `json:"deps" bson:"deps,omitempty"`
	AllKeys  map[string][]string `json:"all_keys" bson:"all_keys,omitempty"`
	Anchors  map[string][]string `json:"anchors" bson:"anchors,omitempty"`
	Noise    []string            `json:"noise" bson:"noise,omitempty"`
}

type TestCaseDB interface {
	Upsert(context.Context, TestCase) error
	UpdateTC(context.Context, TestCase) error
	Get(ctx context.Context, cid, id string) (TestCase, error)
	Delete(ctx context.Context, id string) error
	GetAll(ctx context.Context, cid, app string, anchors bool, offset int, limit int) ([]TestCase, error)
	GetKeys(ctx context.Context, cid, app, uri string) ([]TestCase, error)
	//Exists(context.Context, TestCase) (bool, error)
	DeleteByAnchor(ctx context.Context, cid, app, uri string, filterKeys map[string][]string) error
	GetApps(ctx context.Context, cid string) ([]string, error)
}
