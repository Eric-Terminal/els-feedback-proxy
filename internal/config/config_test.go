package config

import "testing"

func TestValidateAdminListenAddrOnlyAllowsPrivateOrLoopbackIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "局域网 IPv4", address: "192.168.31.102:8521"},
		{name: "回环 IPv4", address: "127.0.0.1:8081"},
		{name: "局域网 IPv6", address: "[fd00::1]:8081"},
		{name: "未配置", address: "", wantErr: true},
		{name: "监听所有 IPv4", address: "0.0.0.0:8081", wantErr: true},
		{name: "监听所有 IPv6", address: "[::]:8081", wantErr: true},
		{name: "公网 IP", address: "1.1.1.1:8081", wantErr: true},
		{name: "主机名", address: "localhost:8081", wantErr: true},
		{name: "缺少端口", address: "192.168.31.102", wantErr: true},
		{name: "端口越界", address: "192.168.31.102:65536", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateAdminListenAddr(test.address)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateAdminListenAddr(%q) 错误状态不符: %v", test.address, err)
			}
		})
	}
}
