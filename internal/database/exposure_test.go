// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestExposures(t *testing.T) {
	if testDB == nil {
		t.Skip("no test DB")
	}
	defer ResetTestDB(t, testDB)
	ctx := context.Background()

	// Insert some Exposures.
	batchTime := time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC).Truncate(time.Microsecond)
	exposures := []*Exposure{
		{
			ExposureKey:     []byte("ABC"),
			Regions:         []string{"US", "CA", "MX"},
			IntervalNumber:  18,
			IntervalCount:   0,
			CreatedAt:       batchTime,
			LocalProvenance: true,
		},
		{
			ExposureKey:     []byte("DEF"),
			Regions:         []string{"CA"},
			IntervalNumber:  118,
			IntervalCount:   1,
			CreatedAt:       batchTime.Add(1 * time.Hour),
			LocalProvenance: true,
		},
		{
			ExposureKey:     []byte("123"),
			IntervalNumber:  218,
			IntervalCount:   2,
			Regions:         []string{"MX", "CA"},
			CreatedAt:       batchTime.Add(2 * time.Hour),
			LocalProvenance: false,
		},
		{
			ExposureKey:     []byte("456"),
			IntervalNumber:  318,
			IntervalCount:   3,
			CreatedAt:       batchTime.Add(3 * time.Hour),
			Regions:         []string{"US"},
			LocalProvenance: false,
		},
	}
	if err := testDB.InsertExposures(ctx, exposures); err != nil {
		t.Fatal(err)
	}

	// Iterate over Exposures, with various criteria.
	for _, test := range []struct {
		criteria IterateExposuresCriteria
		want     []int
	}{
		{
			IterateExposuresCriteria{},
			[]int{0, 1, 2, 3},
		},
		{
			IterateExposuresCriteria{IncludeRegions: []string{"US"}},
			[]int{0, 3},
		},
		{
			IterateExposuresCriteria{ExcludeRegions: []string{"US"}},
			[]int{1, 2},
		},
		{
			IterateExposuresCriteria{IncludeRegions: []string{"CA"}, ExcludeRegions: []string{"MX"}},
			[]int{1},
		},
		{
			IterateExposuresCriteria{SinceTimestamp: exposures[2].CreatedAt},
			[]int{2, 3}, // SinceTimestamp is inclusive
		},
		{
			IterateExposuresCriteria{UntilTimestamp: exposures[2].CreatedAt},
			[]int{0, 1}, // UntilTimestamp is exclusive
		},
		{
			IterateExposuresCriteria{
				IncludeRegions: []string{"CA"},
				ExcludeRegions: []string{"MX"},
				SinceTimestamp: exposures[2].CreatedAt,
			},
			nil,
		},
	} {
		got, err := listExposures(ctx, test.criteria)
		if err != nil {
			t.Fatalf("%+v: %v", test.criteria, err)
		}
		var want []*Exposure
		for _, i := range test.want {
			want = append(want, exposures[i])
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%+v: mismatch (-want, +got):\n%s", test.criteria, diff)
		}
	}

	// Delete some exposures.
	gotN, err := testDB.DeleteExposures(ctx, exposures[2].CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantN := int64(2) // The DeleteExposures time is exclusive, so we expect only the first two were deleted.
	if gotN != wantN {
		t.Errorf("DeleteExposures: deleted %d, want %d", gotN, wantN)
	}
	got, err := listExposures(ctx, IterateExposuresCriteria{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(exposures[2:], got); diff != "" {
		t.Errorf("DeleteExposures: mismatch (-want, +got):\n%s", diff)
	}

}

func listExposures(ctx context.Context, c IterateExposuresCriteria) (_ []*Exposure, err error) {
	var exps []*Exposure
	_, err = testDB.IterateExposures(ctx, c, func(e *Exposure) error {
		exps = append(exps, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return exps, nil
}

func TestIterateExposuresCursor(t *testing.T) {
	if testDB == nil {
		t.Skip("no test DB")
	}
	defer ResetTestDB(t, testDB)
	ctx, cancel := context.WithCancel(context.Background())
	// Insert some Exposures.
	exposures := []*Exposure{
		{
			ExposureKey:    []byte("ABC"),
			Regions:        []string{"US", "CA", "MX"},
			IntervalNumber: 18,
		},
		{
			ExposureKey:    []byte("DEF"),
			Regions:        []string{"CA"},
			IntervalNumber: 118,
		},
		{
			ExposureKey:    []byte("123"),
			IntervalNumber: 218,
			Regions:        []string{"MX", "CA"},
		},
	}
	if err := testDB.InsertExposures(ctx, exposures); err != nil {
		t.Fatal(err)
	}
	// Iterate over them, canceling the context in the middle.
	var seen []*Exposure
	cursor, err := testDB.IterateExposures(ctx, IterateExposuresCriteria{}, func(e *Exposure) error {
		seen = append(seen, e)
		if len(seen) == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, wanted context.Canceled", err)
	}
	if diff := cmp.Diff(exposures[:2], seen); diff != "" {
		t.Fatalf("exposures mismatch (-want, +got):\n%s", diff)
	}
	if want := encodeCursor("2"); cursor != want {
		t.Fatalf("cursor: got %q, want %q", cursor, want)
	}
	// Resume from the cursor.
	ctx = context.Background()
	seen = nil
	cursor, err = testDB.IterateExposures(ctx, IterateExposuresCriteria{LastCursor: cursor},
		func(e *Exposure) error { seen = append(seen, e); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(exposures[2:], seen); diff != "" {
		t.Fatalf("exposures mismatch (-want, +got):\n%s", diff)
	}
	if cursor != "" {
		t.Fatalf("cursor: got %q, want empty", cursor)
	}
}
