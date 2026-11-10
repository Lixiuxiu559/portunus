package configtransfer

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestMutationServiceIdentityAndAdmission(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	id := newUUID()
	cold := NewService(db, id)
	if cold.InstanceID() != id || cold.Ready() {
		t.Fatal("provided instance identity changed")
	}
	_, _, err := cold.Acquire(ctx)
	assertCode(t, err, "TRANSFER_UNAVAILABLE")
	s := testService(t, db)
	leased, release, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, nestedRelease, err := s.Acquire(leased)
	if err != nil {
		t.Fatal("same-context reentry failed", err)
	}
	nestedRelease()
	_, _, err = s.Acquire(ctx)
	assertCode(t, err, "TRANSFER_BUSY")
	release()
	release()
	response, finish := s.ResponseScope(ctx)
	leased, nestedRelease, err = s.Acquire(response)
	if err != nil {
		t.Fatal(err)
	}
	nestedRelease()
	_, _, err = s.Acquire(ctx)
	assertCode(t, err, "TRANSFER_BUSY")
	_, nestedRelease, err = s.Acquire(response)
	if err != nil {
		t.Fatal("response scope could not reenter", err)
	}
	nestedRelease()
	finish()
	finish()
	_, release, err = s.Acquire(ctx)
	if err != nil {
		t.Fatal("finished scope retained lease", err)
	}
	release()
	summary, err := s.Summary(ctx)
	if err != nil || summary.InstanceID != s.InstanceID() || !summary.Ready || summary.CanExport || !summary.Counts.Empty() || summary.Limits["preview_ttl_seconds"] != 900 {
		t.Fatal(summary, err)
	}
}

func TestMutationServiceTwoPreviewAndByteCapacity(t *testing.T) {
	s := testService(t, testDB(t))
	p := samplePayload()
	encoded, err := payloadBytes(&p)
	if err != nil {
		t.Fatal(err)
	}
	// A JSON document may include trailing whitespace up to the plaintext cap.
	raw := append(encoded, bytes.Repeat([]byte{' '}, MaxPlaintextBytes-len(encoded))...)
	for i := 0; i < 2; i++ {
		if _, err := s.prepare(context.Background(), &p, append([]byte{}, raw...), strings.Repeat("a", 64)); err != nil {
			t.Fatal("two exact-size previews must fit", i, err)
		}
	}
	_, err = s.prepare(context.Background(), &p, encoded, strings.Repeat("a", 64))
	assertCode(t, err, "LIMIT_EXCEEDED")
	s.releaseAll()
}

func TestMutationCommitNameConflictRequiresNewConfirmation(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := testService(t, db)
	d := preparedMutationPreview(t, s)
	if err := db.Table("groups").Create(map[string]any{"name": "coding", "strategy": "manual"}).Error; err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Commit(ctx, d.OperationID, confirmation(d))
	assertCode(t, err, "PREVIEW_CHANGED")
	replacement := err.(*Error).ReplacementPreview
	if replacement == nil || replacement.OperationID == d.OperationID || replacement.Groups[0].Name != "coding（导入）" {
		t.Fatal(replacement)
	}
	var n int64
	db.Table("channels").Count(&n)
	if n != 0 {
		t.Fatal("stale confirmation wrote configuration")
	}
	if _, _, err := s.Commit(ctx, replacement.OperationID, confirmation(*replacement)); err != nil {
		t.Fatal(err)
	}
	d = preparedMutationPreview(t, s)
	value, _, err := s.Commit(ctx, d.OperationID, confirmation(d))
	if err != nil {
		t.Fatal(err)
	}
	result := value.(ResultDTO)
	if len(result.Channels) != 2 || result.Channels[0].Ref != "c1" || result.Channels[0].Name != "A（导入 2）" || result.Channels[0].OriginalName != "A" || result.Channels[0].ID == "" || result.RenameCount != 5 {
		t.Fatal("renamed channel result incomplete", result)
	}
}

func TestMutationRestoreIDSafeIntegerBoundary(t *testing.T) {
	db := testDB(t)
	if err := db.Exec("INSERT INTO sqlite_sequence(name,seq) VALUES ('channels',?)", MaxSafeID-1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		id, err := insertID(tx, "channels", map[string]any{"name": "boundary"}, map[string]any{"name": "boundary"})
		if err != nil || id != MaxSafeID {
			t.Fatal("maximum safe id rejected", id, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := insertID(tx, "channels", map[string]any{"name": "overflow"}, map[string]any{"name": "overflow"})
		return err
	})
	assertCode(t, err, "LIMIT_EXCEEDED")
	var n int64
	db.Table("channels").Where("name=?", "overflow").Count(&n)
	if n != 0 {
		t.Fatal("unsafe id was committed")
	}
}
