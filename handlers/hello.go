package handlers

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

// HelloHandler: PocketBase에서 쓸 핸들러 함수
func HelloHandler(re *core.RequestEvent) error {
	return re.String(http.StatusOK, "hello world!")
}