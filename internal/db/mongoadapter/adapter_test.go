package mongoadapter

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"tdb/internal/config"
	"tdb/internal/db"
)

func TestBuildMongoURIUsesBasicDirectConnection(t *testing.T) {
	profile := config.Profile{
		Host:      "127.0.0.1",
		Port:      27017,
		User:      "root",
		Password:  "secret",
		Database:  "app",
		AuthDB:    "admin",
		URIParams: "retryWrites=false",
	}

	got := BuildMongoURI(profile)
	want := "mongodb://root:secret@127.0.0.1:27017/app?authSource=admin&retryWrites=false"
	if got != want {
		t.Fatalf("BuildMongoURI() = %q, want %q", got, want)
	}
}

func TestIDFilterParsesObjectIDWhenPossible(t *testing.T) {
	filter, err := IDFilter(db.Key{ID: "507f1f77bcf86cd799439011"})
	if err != nil {
		t.Fatalf("IDFilter returned error: %v", err)
	}
	objectID, ok := filter["_id"].(bson.ObjectID)
	if !ok {
		t.Fatalf("_id type = %T, want primitive.ObjectID", filter["_id"])
	}
	if objectID.Hex() != "507f1f77bcf86cd799439011" {
		t.Fatalf("objectID = %s", objectID.Hex())
	}
}

func TestIndexDocumentsToMetadata(t *testing.T) {
	metadata := IndexDocumentsToMetadata([]bson.M{
		{"name": "_id_", "key": bson.M{"_id": int32(1)}, "unique": true},
		{"name": "email_1", "key": bson.M{"email": int32(1)}},
	})

	if len(metadata.Indexes) != 2 {
		t.Fatalf("indexes = %+v, want 2", metadata.Indexes)
	}
	if metadata.Indexes[0].Name != "_id_" || metadata.Indexes[0].Columns[0] != "_id" || !metadata.Indexes[0].Unique {
		t.Fatalf("first index = %+v", metadata.Indexes[0])
	}
	if metadata.Indexes[1].Name != "email_1" || metadata.Indexes[1].Columns[0] != "email" || metadata.Indexes[1].Unique {
		t.Fatalf("second index = %+v", metadata.Indexes[1])
	}
}

func TestParseMongoshQueryMethods(t *testing.T) {
	for _, tc := range []struct {
		text       string
		method     string
		collection string
		filterKey  string
		limit      int
	}{
		{text: "db.users.find({status: 'active'})", method: "find", collection: "users", filterKey: "status"},
		{text: "db.users.findOne({room_id: 1})", method: "findOne", collection: "users", filterKey: "room_id", limit: 1},
		{text: "db.users.countDocuments({status: 'active'})", method: "countDocuments", collection: "users", filterKey: "status"},
		{text: "db.users.aggregate([{$match: {status: 'active'}}])", method: "aggregate", collection: "users"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			command, err := ParseMongoshCommand(tc.text)
			if err != nil {
				t.Fatalf("ParseMongoshCommand returned error: %v", err)
			}
			if command.Method != tc.method || command.Collection != tc.collection {
				t.Fatalf("command = %+v, want %s/%s", command, tc.method, tc.collection)
			}
			if tc.filterKey != "" {
				if _, ok := command.Filter[tc.filterKey]; !ok {
					t.Fatalf("filter = %+v, want key %s", command.Filter, tc.filterKey)
				}
			}
			if command.Limit != tc.limit {
				t.Fatalf("Limit = %d, want %d", command.Limit, tc.limit)
			}
		})
	}
}

func TestExecuteMongoshQueryUsesMongoshParserBeforeJSONFallback(t *testing.T) {
	adapter := NewWithClient(config.Profile{}, nil)

	_, err := adapter.Execute(context.Background(), db.Command{Text: "db.users.find({})", Database: "app"})
	if !errors.Is(err, ErrNoClient) {
		t.Fatalf("Execute error = %v, want ErrNoClient from mongosh path", err)
	}
}

func TestParseMongoshCRUDMethods(t *testing.T) {
	insert, err := ParseMongoshCommand("db.users.insertOne({name: 'Ada'})")
	if err != nil {
		t.Fatalf("parse insertOne: %v", err)
	}
	if insert.Method != "insertOne" || insert.Document["name"] != "Ada" {
		t.Fatalf("insert command = %+v, want name Ada", insert)
	}

	update, err := ParseMongoshCommand("db.users.updateOne({name: 'Ada'}, {$set: {name: 'Grace'}})")
	if err != nil {
		t.Fatalf("parse updateOne: %v", err)
	}
	if update.Method != "updateOne" || update.Filter["name"] != "Ada" {
		t.Fatalf("update command = %+v, want filter name Ada", update)
	}
	if _, ok := update.Update["$set"]; !ok {
		t.Fatalf("update document = %+v, want $set", update.Update)
	}

	deleteCommand, err := ParseMongoshCommand("db.users.deleteMany({name: 'Ada'})")
	if err != nil {
		t.Fatalf("parse deleteMany: %v", err)
	}
	if deleteCommand.Method != "deleteMany" || deleteCommand.Filter["name"] != "Ada" {
		t.Fatalf("delete command = %+v, want filter name Ada", deleteCommand)
	}
}

func TestExecuteMongoshCRUDRespectsReadOnly(t *testing.T) {
	adapter := NewWithClient(config.Profile{ReadOnly: true}, nil)

	_, err := adapter.Execute(context.Background(), db.Command{Text: "db.users.insertOne({name: 'Ada'})", Database: "app"})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Execute insertOne error = %v, want ErrReadOnly", err)
	}
}

func TestAdapterRejectsWritesForReadOnlyProfile(t *testing.T) {
	adapter := NewWithClient(config.Profile{ReadOnly: true}, nil)

	_, err := adapter.Delete(context.Background(), db.Target{Name: "users"}, db.Key{ID: "abc"})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Delete error = %v, want ErrReadOnly", err)
	}
}

func TestExecuteEnforcesReadOnlyForWrites(t *testing.T) {
	a := NewWithClient(config.Profile{ReadOnly: true, Database: "app"}, nil)
	if _, err := a.Execute(context.Background(), db.Command{Text: "db.users.deleteOne({})"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("deleteOne on read-only conn = %v, want ErrReadOnly", err)
	}
	if _, err := a.Execute(context.Background(), db.Command{Text: "db.users.insertOne({a:1})"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("insertOne on read-only conn = %v, want ErrReadOnly", err)
	}
	// A find passes the read-only gate (then fails on the nil client).
	if _, err := a.Execute(context.Background(), db.Command{Text: "db.users.find({})"}); errors.Is(err, ErrReadOnly) {
		t.Fatalf("find on read-only conn must not be rejected as read-only")
	}
}
