package store

import (
	"path/filepath"
	"testing"
	"time"

	"filesync/internal/model"

	"github.com/google/uuid"
)

func newTestUploadRepo(t *testing.T) *UploadRepo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewUploadRepo(db)
}

func TestUploadRepoCreateGetUpdateDelete(t *testing.T) {
	ur := newTestUploadRepo(t)
	now := time.Now()
	u := &model.Upload{
		ID: uuid.NewString(), Name: "a.bin", Size: 20, ChunkSize: 8, ChunkCount: 3,
		Received: 0, CreatedAt: now, UpdatedAt: now,
	}
	if err := ur.Create(u); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	got, err := ur.Get(u.ID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Name != "a.bin" || got.Size != 20 || got.ChunkCount != 3 {
		t.Errorf("Get 返回字段不符: %+v", got)
	}

	// 更新 received（单调递增）
	if err := ur.UpdateReceived(u.ID, 8); err != nil {
		t.Fatalf("UpdateReceived 失败: %v", err)
	}
	// 回退不应生效（MAX 保护）
	if err := ur.UpdateReceived(u.ID, 4); err != nil {
		t.Fatalf("UpdateReceived 回退失败: %v", err)
	}
	got, _ = ur.Get(u.ID)
	if got.Received != 8 {
		t.Errorf("received 应保持 8，得到 %d", got.Received)
	}

	if err := ur.Delete(u.ID); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := ur.Get(u.ID); err == nil {
		t.Error("删除后 Get 应报错")
	}
}

func TestUploadRepoListStale(t *testing.T) {
	ur := newTestUploadRepo(t)
	now := time.Now()

	fresh := &model.Upload{ID: uuid.NewString(), Name: "f.bin", Size: 10, ChunkSize: 8, ChunkCount: 2, Received: 0, CreatedAt: now, UpdatedAt: now}
	stale := &model.Upload{ID: uuid.NewString(), Name: "s.bin", Size: 10, ChunkSize: 8, ChunkCount: 2, Received: 0, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour)}
	if err := ur.Create(fresh); err != nil {
		t.Fatalf("Create fresh 失败: %v", err)
	}
	if err := ur.Create(stale); err != nil {
		t.Fatalf("Create stale 失败: %v", err)
	}

	ids, err := ur.ListStale(now.Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("ListStale 失败: %v", err)
	}
	if len(ids) != 1 || ids[0] != stale.ID {
		t.Errorf("应只返回过期会话，得到 %v", ids)
	}
}
