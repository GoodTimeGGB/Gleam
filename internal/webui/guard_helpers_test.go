package webui

import (
	"net/http"
	"net/http/httptest"
)

// newTokenTestServer 起一个带守卫的测试服务，并在服务端入口替请求补上口令头。
//
// 为什么在服务端补而不是让每个测试的客户端带：现有几十处调用各自拼请求，逐个改既啰嗦
// 又容易漏；而守卫的 Host / Origin / Content-Type 三条校验仍然原样生效——
// 这些集成测试顺带证明了「正常的前端请求形态能过守卫」。口令本身的拒绝路径由 guard_test.go 单独覆盖。
func newTokenTestServer(srv *Server) *httptest.Server {
	h := srv.Handler()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(TokenHeader) == "" {
			r.Header.Set(TokenHeader, srv.Token())
		}
		h.ServeHTTP(w, r)
	}))
}
