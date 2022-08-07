package mgo

import (
	"context"
	"time"

	"go.keploy.io/server/pkg/service/run"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/keploy/go-sdk/integrations/kmongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

func NewRun(c *kmongo.Collection, test *kmongo.Collection, log *zap.Logger) *RunDB {
	return &RunDB{
		c:    c,
		log:  log,
		test: test,
	}
}

type RunDB struct {
	c    *kmongo.Collection
	test *kmongo.Collection
	log  *zap.Logger
}

func (r *RunDB) ReadTest(ctx context.Context, id string) (run.Test, error) {

	// too repetitive
	// TODO write a generic FindOne for all get calls
	filter := bson.M{"_id": id}
	var t run.Test
	err := r.test.FindOne(ctx, filter).Decode(&t)
	if err != nil {
		return t, err
	}
	return t, nil
}

func (r *RunDB) ReadTests(ctx context.Context, runID string) ([]run.Test, error) {

	filter := bson.M{"run_id": runID}
	findOptions := options.Find()

	var res []run.Test
	cur, err := r.test.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, err
	}

	// Loop through the cursor
	for cur.Next(ctx) {
		var t run.Test
		err = cur.Decode(&t)
		if err != nil {
			return nil, err
		}
		res = append(res, t)
	}

	if err = cur.Err(); err != nil {
		return nil, err

	}

	err = cur.Close(ctx)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (r *RunDB) PutTest(ctx context.Context, t run.Test) error {

	upsert := true
	opt := &options.UpdateOptions{
		Upsert: &upsert,
	}
	filter := bson.M{"_id": t.ID}
	update := bson.D{{"$set", t}}

	_, err := r.test.UpdateOne(ctx, filter, update, opt)
	if err != nil {
		//t.log.Error("failed to insert testcase into DB", zap.String("cid", tc.CID), zap.String("appid", tc.AppID), zap.String("id", tc.ID), zap.Error())
		return err
	}
	return nil
}

func (r *RunDB) Read(ctx context.Context, cid string, user, app, id *string, from, to *time.Time, offset int, limit int) ([]*run.TestRun, error) {

	filter := bson.M{
		"cid": cid,
	}
	if user != nil {
		filter["user"] = user
	}

	if app != nil {
		filter["app"] = app
	}
	if id != nil {
		filter["_id"] = id
	}

	if from != nil {
		filter["updated"] = bson.M{"$gte": from.Unix()}
	}

	if to != nil {
		filter["updated"] = bson.M{"$lte": to.Unix()}
	}

	var tcs []*run.TestRun
	opt := options.Find()

	opt.SetSort(bson.M{"created": -1}) //for descending order
	opt.SetSkip(int64(offset))
	opt.SetLimit(int64(limit))

	cur, err := r.c.Find(ctx, filter, opt)
	if err != nil {
		return nil, err
	}

	// Loop through the cursor
	for cur.Next(ctx) {
		var tc *run.TestRun
		err = cur.Decode(&tc)
		if err != nil {
			return nil, err

		}
		tcs = append(tcs, tc)
	}

	if err = cur.Err(); err != nil {
		return nil, err

	}

	err = cur.Close(ctx)
	if err != nil {
		return nil, err
	}
	return tcs, nil
}

func (r *RunDB) Upsert(ctx context.Context, testRun run.TestRun) error {

	upsert := true
	opt := &options.UpdateOptions{
		Upsert: &upsert,
	}
	filter := bson.M{"_id": testRun.ID}
	update := bson.D{{"$set", testRun}}

	_, err := r.c.UpdateOne(ctx, filter, update, opt)
	if err != nil {
		//t.log.Error("failed to insert testcase into DB", zap.String("cid", tc.CID), zap.String("appid", tc.AppID), zap.String("id", tc.ID), zap.Error())
		return err
	}
	return nil
}

func (r *RunDB) Increment(ctx context.Context, success, failure bool, id string) error {

	update := bson.M{}
	if success {
		update["$inc"] = bson.D{{"success", 1}}
	}

	if failure {
		update["$inc"] = bson.D{{"failure", 1}}
	}

	_, err := r.c.UpdateOne(ctx, bson.M{
		"_id": id,
	}, update, options.Update().SetUpsert(true))

	if err != nil {
		return err
	}
	return nil
}
