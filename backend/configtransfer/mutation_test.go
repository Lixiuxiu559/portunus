package configtransfer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Recovery tests enter at the already validated payload boundary. Encryption is
// tested separately, so database/state-machine mutations need not redo Argon2.
func preparedMutationPreview(t *testing.T, s *Service) PreviewDTO {
	t.Helper()
	p := samplePayload()
	b, err := payloadBytes(&p)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.prepare(context.Background(), &p, b, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMutationRestoreRoundTripAndReplay(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := testService(t, db)
	d := preparedMutationPreview(t, s)
	value, status, err := s.Commit(ctx, d.OperationID, confirmation(d))
	if err != nil || status != 200 {
		t.Fatal(value, status, err)
	}
	result := value.(ResultDTO)
	if result.State != "committed" || result.OperationID != d.OperationID || result.Counts != (Counts{2, 3, 3}) || result.Replayed || len(result.Groups) != 3 {
		t.Fatal(result)
	}
	got, err := snapshot(ctx, db, samplePayload().AppVersion)
	if err != nil {
		t.Fatal(err)
	}
	want := samplePayload()
	if !reflect.DeepEqual(got, &want) {
		t.Fatalf("restored payload changed: got=%+v want=%+v", got, want)
	}
	value, status, err = s.Commit(ctx, d.OperationID, confirmation(d))
	if err != nil || status != 200 || !value.(ResultDTO).Replayed || !reflect.DeepEqual(value.(ResultDTO).Groups, result.Groups) {
		t.Fatal(value, status, err)
	}
	r, err := s.receipt(ctx, d.OperationID)
	if err != nil || r.Status != "committed" || r.ClaimStartedAtMs == nil || r.CommittedAtMs == nil || r.ConfirmedAtMs == nil {
		t.Fatal(r, err)
	}
	s.mu.Lock()
	remaining := len(s.previews)
	retained := s.retainedBytes
	s.mu.Unlock()
	if remaining != 0 || retained != 0 {
		t.Fatal("committed source retained", remaining, retained)
	}
	if err := s.Ack(ctx, d.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(ctx, d.OperationID); err != nil {
		t.Fatal(err)
	}
	list, err := s.RecoveryList(ctx, "", 20)
	if err != nil || len(list.Data) != 0 {
		t.Fatal(list, err)
	}
}

func TestMutationRestoreRollback(t *testing.T) {
	for _, stage := range []string{"channels", "models", "groups", "items", "receipt"} {
		t.Run(stage, func(t *testing.T) {
			db := testDB(t)
			s := testService(t, db)
			d := preparedMutationPreview(t, s)
			s.fault = func(at string) error {
				if at == stage {
					return errors.New("synthetic rollback")
				}
				return nil
			}
			value, status, err := s.Commit(context.Background(), d.OperationID, confirmation(d))
			if err != nil || status != 200 || value.(StateDTO).State != "aborted" || value.(StateDTO).FailureCode != "IMPORT_FAILED" {
				t.Fatal(value, status, err)
			}
			for _, table := range []string{"channels", "models", "groups", "group_items"} {
				var n int64
				if err := db.Table(table).Count(&n).Error; err != nil || n != 0 {
					t.Fatal("partial rollback", table, n, err)
				}
			}
			_, _, err = s.Commit(context.Background(), d.OperationID, confirmation(d))
			assertCode(t, err, "OPERATION_ABORTED")
		})
	}
}

func TestMutationRecoveryTransitionsAndSecretCleanup(t *testing.T) {
	for _, kind := range []string{"live", "confirmed", "lost", "expired", "foreign owner"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t)
			s := testService(t, db)
			d := preparedMutationPreview(t, s)
			s.mu.Lock()
			raw := s.previews[d.PreviewID].raw
			s.mu.Unlock()
			switch kind {
			case "confirmed":
				db.Exec("UPDATE config_import_receipts SET confirmed_at_ms=? WHERE operation_id=?", time.Now().UnixMilli(), d.OperationID)
			case "foreign owner":
				db.Exec("UPDATE config_import_receipts SET owner_instance_id=? WHERE operation_id=?", newUUID(), d.OperationID)
			case "lost":
				s.mu.Lock()
				s.releasePreviewLocked(d.PreviewID)
				s.mu.Unlock()
			case "expired":
				s.mu.Lock()
				s.previews[d.PreviewID].deadline = time.Now().Add(-time.Second)
				s.mu.Unlock()
			}
			value, status, err := s.Recover(ctx, d.OperationID)
			if err != nil || status != 200 {
				t.Fatal(value, status, err)
			}
			state := value.(StateDTO)
			if kind == "live" {
				if state.State != "prepared" || state.ExpiresAt == "" || bytes.Equal(raw, make([]byte, len(raw))) {
					t.Fatal("live preview changed", state)
				}
				if err := s.Ack(ctx, d.OperationID); err == nil {
					t.Fatal("uncommitted operation acknowledged")
				}
			} else {
				if state.State != "aborted" || state.FailureCode != "SESSION_LOST" || state.Stage != "recovery" || !bytes.Equal(raw, make([]byte, len(raw))) {
					t.Fatal("lost session not safely terminated", state)
				}
				s.mu.Lock()
				remaining, retained := len(s.previews), s.retainedBytes
				s.mu.Unlock()
				if remaining != 0 || retained != 0 || s.isRunning(d.OperationID) {
					t.Fatal("terminal preview not released", remaining, retained)
				}
			}
		})
	}
}

func TestMutationStoredResultRejectsInconsistentProof(t *testing.T) {
	for _, field := range []string{"invalid json", "operation_id", "state", "dto version", "counts", "rename_count"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t)
			s := testService(t, db)
			d := preparedMutationPreview(t, s)
			value, _, err := s.Commit(ctx, d.OperationID, confirmation(d))
			if err != nil {
				t.Fatal(err)
			}
			result := value.(ResultDTO)
			column, stored := "result_json", any(nil)
			switch field {
			case "invalid json":
				stored = "{"
			case "operation_id":
				result.OperationID = newUUID()
			case "state":
				result.State = "aborted"
			case "dto version":
				result.ResultVersion = 2
			case "counts":
				result.Counts.Models++
			case "rename_count":
				result.RenameCount++
			}
			if stored == nil {
				b, _ := json.Marshal(result)
				stored = string(b)
			}
			if err := db.Table("config_import_receipts").Where("operation_id=?", d.OperationID).Update(column, stored).Error; err != nil {
				t.Fatal(err)
			}
			_, _, err = s.Result(ctx, d.OperationID)
			assertCode(t, err, "RESULT_UNCONFIRMED")
		})
	}
}

func TestMutationRecoveryPaginationAndConfirmationIdentity(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := testService(t, db)
	d := preparedMutationPreview(t, s)
	for _, field := range []string{"preview", "file", "plan"} {
		r := confirmation(d)
		switch field {
		case "preview":
			r.PreviewID = "!"
		case "file":
			r.FileSHA256 = strings.Repeat("b", 64)
		case "plan":
			r.PlanSHA256 = strings.Repeat("b", 64)
		}
		_, _, err := s.Commit(ctx, d.OperationID, r)
		if err == nil {
			t.Fatal("mismatched confirmation accepted", field)
		}
	}
	if _, err := s.Discard(ctx, d.PreviewID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		d = preparedMutationPreview(t, s)
		if _, _, err := s.Commit(ctx, d.OperationID, confirmation(d)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 3; i++ {
		page, err := s.RecoveryList(ctx, cursor, 1)
		if err != nil || len(page.Data) != 1 || seen[page.Data[0].OperationID] || page.Data[0].State != "committed" || page.Data[0].Counts != (Counts{2, 3, 3}) || page.Data[0].ConfirmedAt == "" {
			t.Fatal(page, err)
		}
		seen[page.Data[0].OperationID] = true
		cursor = page.NextCursor
		if (i < 2) != (cursor != "") {
			t.Fatal("incorrect pagination continuation", page)
		}
	}
	for _, cursor := range []string{"!", base64.RawURLEncoding.EncodeToString([]byte(`{"created_at_ms":-1,"operation_id":"` + newUUID() + `"}`)), strings.Repeat("a", 257)} {
		_, err := s.RecoveryList(ctx, cursor, 20)
		assertCode(t, err, "INVALID_INPUT")
	}
	for _, n := range []int{0, 101} {
		_, err := s.RecoveryList(ctx, "", n)
		assertCode(t, err, "INVALID_INPUT")
	}
}
