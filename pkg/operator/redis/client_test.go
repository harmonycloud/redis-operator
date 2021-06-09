package redis

import (
	"testing"
)

func Test_client_IsMaster(t *testing.T) {
	type args struct {
		ip       string
		password string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			args: args{
				ip:       "10.244.102.129",
				password: "dangerous",
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		for i := 0; i < 10; i++ {
			t.Run(tt.name, func(t *testing.T) {
				c := &client{}
				got, err := c.IsMaster(tt.args.ip, tt.args.password)
				if (err != nil) != tt.wantErr {
					t.Errorf("IsMaster() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if got != tt.want {
					t.Errorf("IsMaster() got = %v, want %v", got, tt.want)
				}
			})
			//time.Sleep(time.Second * 2)
		}
	}
}
