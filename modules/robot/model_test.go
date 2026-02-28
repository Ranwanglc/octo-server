package robot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInlineQueryResult_Check(t *testing.T) {
	tests := []struct {
		name    string
		result  InlineQueryResult
		wantErr bool
	}{
		{
			name:    "valid with type",
			result:  InlineQueryResult{Type: ResultTypeGIF, ID: "1"},
			wantErr: false,
		},
		{
			name:    "valid with custom type",
			result:  InlineQueryResult{Type: "image", ID: "2"},
			wantErr: false,
		},
		{
			name:    "empty type",
			result:  InlineQueryResult{Type: "", ID: "3"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.Check()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "type不能为空")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResultType_Constants(t *testing.T) {
	assert.Equal(t, ResultType("gif"), ResultTypeGIF)
}

func TestInlineQueryResult_Fields(t *testing.T) {
	result := InlineQueryResult{
		InlineQuerySID: "sid_001",
		Type:           ResultTypeGIF,
		ID:             "result_001",
		Results: []map[string]interface{}{
			{"url": "https://example.com/1.gif"},
			{"url": "https://example.com/2.gif"},
		},
		NextOffset: "page2",
	}

	assert.Equal(t, "sid_001", result.InlineQuerySID)
	assert.Equal(t, ResultTypeGIF, result.Type)
	assert.Equal(t, "result_001", result.ID)
	assert.Len(t, result.Results, 2)
	assert.Equal(t, "page2", result.NextOffset)
}

func TestInlineQuery_Fields(t *testing.T) {
	q := InlineQuery{
		SID:         "sid_001",
		ChannelID:   "ch_001",
		ChannelType: 2,
		FromUID:     "uid_001",
		Query:       "test query",
		Offset:      "0",
	}

	assert.Equal(t, "sid_001", q.SID)
	assert.Equal(t, "ch_001", q.ChannelID)
	assert.Equal(t, uint8(2), q.ChannelType)
	assert.Equal(t, "uid_001", q.FromUID)
	assert.Equal(t, "test query", q.Query)
	assert.Equal(t, "0", q.Offset)
}

func TestGifResult_Fields(t *testing.T) {
	gif := GifResult{
		URL:    "https://example.com/test.gif",
		Width:  320,
		Height: 240,
	}

	assert.Equal(t, "https://example.com/test.gif", gif.URL)
	assert.Equal(t, 320, gif.Width)
	assert.Equal(t, 240, gif.Height)
}

func TestMessageReq_Fields(t *testing.T) {
	req := MessageReq{
		Setting:     1,
		ChannelID:   "ch_001",
		ChannelType: 2,
		StreamNo:    "stream_001",
		Entities: []*Entitiy{
			{Length: 5, Offset: 0, Type: "mention"},
		},
		Payload: map[string]interface{}{
			"content": "hello",
		},
	}

	assert.Equal(t, uint8(1), req.Setting)
	assert.Equal(t, "ch_001", req.ChannelID)
	assert.Equal(t, uint8(2), req.ChannelType)
	assert.Equal(t, "stream_001", req.StreamNo)
	assert.Len(t, req.Entities, 1)
	assert.Equal(t, "mention", req.Entities[0].Type)
}

func TestTypingReq_Fields(t *testing.T) {
	req := TypingReq{
		ChannelID:   "ch_001",
		ChannelType: 1,
	}

	assert.Equal(t, "ch_001", req.ChannelID)
	assert.Equal(t, uint8(1), req.ChannelType)
}
