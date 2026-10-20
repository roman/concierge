package heartbeat

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestStartupActivationPreservesExistingScheduler(t *testing.T) {
	ctx := context.Background()
	store := Store{Root: initializedRoot(t)}
	now := time.Unix(100, 0)
	if err := store.EnableForStartup(ctx, now); err != nil {
		t.Fatal(err)
	}
	first, err := store.Read(ctx)
	if err != nil || !first.Enabled || first.Paused {
		t.Fatalf("startup activation = %+v, %v", first, err)
	}
	if err := store.Pause(ctx, "operator maintenance"); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Read(ctx)
	if err := store.EnableForStartup(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf(
			"repeat startup changed scheduler: before=%+v after=%+v error=%v",
			before,
			after,
			err,
		)
	}
}
