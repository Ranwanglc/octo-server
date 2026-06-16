package messages_search

import "testing"

func TestCheckTransportSecurity(t *testing.T) {
	cases := []struct {
		name    string
		cfg     SearchConfig
		wantErr bool
	}{
		{
			name: "no credentials, remote http ok",
			cfg:  SearchConfig{OSAddrs: []string{"http://os.internal:9200"}},
		},
		{
			name:    "credentials over remote http rejected",
			cfg:     SearchConfig{OSAddrs: []string{"http://os.internal:9200"}, OSUsername: "u"},
			wantErr: true,
		},
		{
			name: "credentials over https ok",
			cfg:  SearchConfig{OSAddrs: []string{"https://os.internal:9200"}, OSUsername: "u"},
		},
		{
			name: "credentials over localhost http ok",
			cfg:  SearchConfig{OSAddrs: []string{"http://localhost:9200"}, OSUsername: "u"},
		},
		{
			name: "credentials over 127.0.0.1 http ok",
			cfg:  SearchConfig{OSAddrs: []string{"http://127.0.0.1:9200"}, OSUsername: "u"},
		},
		{
			name: "mixed addrs: one remote http poisons the set",
			cfg: SearchConfig{
				OSAddrs:    []string{"https://os1.internal:9200", "http://os2.internal:9200"},
				OSUsername: "u",
			},
			wantErr: true,
		},
		{
			name: "explicit insecure opt-in allows remote http",
			cfg: SearchConfig{
				OSAddrs:        []string{"http://os.internal:9200"},
				OSUsername:     "u",
				OSInsecureHTTP: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkTransportSecurity(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
