package cachekv

import (
	"context"
	"testing"
	"time"
)

func TestMemKV(t *testing.T) {
	kv := MemKV()

	ctx := context.TODO()

	ok, val, err := kv.Get(ctx, "aaa")

	if ok != false || val != "" || err != nil {
		t.Fatal("step get empty")
	}

	err = kv.Set(ctx, "bbb", "asd", time.Second)
	if err != nil {
		t.Fatal("step set")
	}

	ok, val, err = kv.Get(ctx, "bbb")
	if ok != true || val != "asd" || err != nil {
		t.Fatal("step get after set immediately")
	}

	time.AfterFunc(time.Second*2, func() {
		if ok != false || val != "" || err != nil {
			t.Fatal("step get a while")
		}
	})
}
