package controller

import (
	"testing"

	"github.com/mnestor/ssoossh/server/utils/paging"
)

// TestNewPageMeta covers the arithmetic every paged list endpoint echoes
// back. It lives here rather than in server/utils/paging because the
// conversion to the wire shape is what the frontend's pager reads, and the
// "empty list is page 1 of 1" boundary is the one a UI would otherwise have
// to special-case.
func TestNewPageMeta(t *testing.T) {
	tests := []struct {
		name          string
		params        paging.Params
		total         int64
		wantPage      int
		wantPageCount int
	}{
		{
			name:          "should report a single empty page for an empty list",
			params:        paging.Params{Limit: 25},
			total:         0,
			wantPage:      1,
			wantPageCount: 1,
		},
		{
			name:          "should report the page the offset lands on",
			params:        paging.Params{Limit: 25, Offset: 50},
			total:         120,
			wantPage:      3,
			wantPageCount: 5,
		},
		{
			name:          "should not add an empty page for an exact multiple",
			params:        paging.Params{Limit: 20},
			total:         40,
			wantPage:      1,
			wantPageCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newPageMeta(tt.params, tt.total)

			if got.Total != tt.total {
				t.Errorf("Total = %d, want %d", got.Total, tt.total)
			}
			if got.Limit != tt.params.Limit {
				t.Errorf("Limit = %d, want %d", got.Limit, tt.params.Limit)
			}
			if got.Offset != tt.params.Offset {
				t.Errorf("Offset = %d, want %d", got.Offset, tt.params.Offset)
			}
			if got.Page != tt.wantPage {
				t.Errorf("Page = %d, want %d", got.Page, tt.wantPage)
			}
			if got.PageCount != tt.wantPageCount {
				t.Errorf("PageCount = %d, want %d", got.PageCount, tt.wantPageCount)
			}
		})
	}
}
