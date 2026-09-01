package extraction

import (
	"context"
	"errors"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type profileDeleteRepo struct {
	port.ExtractionPipelineRepo
	profile *model.ExtractionProfile
	usage   int
	deleted bool
}

func (r *profileDeleteRepo) GetExtractionProfile(_ context.Context, id string) (*model.ExtractionProfile, error) {
	if r.profile == nil || r.profile.ID != id {
		return nil, errors.New("not found")
	}
	clone := *r.profile
	return &clone, nil
}

func (r *profileDeleteRepo) CountExtractionProfileUsage(_ context.Context, _ string) (int, error) {
	return r.usage, nil
}

func (r *profileDeleteRepo) DeleteExtractionProfile(_ context.Context, id string) (bool, error) {
	if r.profile == nil || r.profile.ID != id {
		return false, nil
	}
	r.deleted = true
	return true, nil
}

func TestDeleteExtractionProfileGuardsUsage(t *testing.T) {
	ctx := context.Background()
	profile := &model.ExtractionProfile{ID: "profile-1", Name: "p"}
	repo := &profileDeleteRepo{profile: profile, usage: 3}
	service := &Service{repo: repo}

	if _, err := service.DeleteProfile(ctx, profile.ID); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("want ErrProfileInUse, got %v", err)
	}
	if repo.deleted {
		t.Fatal("profile must not be deleted while in use")
	}

	repo.usage = 0
	deleted, err := service.DeleteProfile(ctx, profile.ID)
	if err != nil || !deleted {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
	if !repo.deleted {
		t.Fatal("unused profile should be deleted")
	}

	if _, err := service.DeleteProfile(ctx, "missing"); err == nil {
		t.Fatal("deleting a missing profile should fail")
	}
}
