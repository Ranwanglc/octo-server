package report

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReportReq_Check(t *testing.T) {
	tests := []struct {
		name    string
		req     reportReq
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid report",
			req: reportReq{
				ChannelID:   "ch_001",
				ChannelType: 1,
				CategoryNo:  "cat_001",
			},
			wantErr: false,
		},
		{
			name: "valid with images and remark",
			req: reportReq{
				ChannelID:   "ch_001",
				ChannelType: 2,
				CategoryNo:  "cat_001",
				Imgs:        []string{"img1.jpg", "img2.jpg"},
				Remark:      "举报说明",
			},
			wantErr: false,
		},
		{
			name: "empty channel ID",
			req: reportReq{
				ChannelID:   "",
				ChannelType: 1,
				CategoryNo:  "cat_001",
			},
			wantErr: true,
			errMsg:  "频道ID不能为空",
		},
		{
			name: "zero channel type",
			req: reportReq{
				ChannelID:   "ch_001",
				ChannelType: 0,
				CategoryNo:  "cat_001",
			},
			wantErr: true,
			errMsg:  "频道类型不能为空",
		},
		{
			name: "empty category no",
			req: reportReq{
				ChannelID:   "ch_001",
				ChannelType: 1,
				CategoryNo:  "",
			},
			wantErr: true,
			errMsg:  "举报类别不能为空",
		},
		{
			name: "all fields empty",
			req: reportReq{
				ChannelID:   "",
				ChannelType: 0,
				CategoryNo:  "",
			},
			wantErr: true,
			errMsg:  "频道ID不能为空",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.check()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
