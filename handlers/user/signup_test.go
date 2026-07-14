package user

import (
	"errors"
	"testing"
)

func TestIsUsernameUniqueConstraintError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "pocketbase unique index error",
			err:  errors.New("UNIQUE constraint failed: index idx_username_lower__pb_users_auth_"),
			want: true,
		},
		{
			name: "validation unique message on username",
			err:  errors.New("username: Value must be unique."),
			want: true,
		},
		{
			name: "other validation message",
			err:  errors.New("nickname: Value is required."),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isUsernameUniqueConstraintError(tc.err)
			if got != tc.want {
				t.Fatalf("expected %v, got %v (err=%v)", tc.want, got, tc.err)
			}
		})
	}
}
