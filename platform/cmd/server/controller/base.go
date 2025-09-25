package controller

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

type BaseController struct{}

type ErrCode int

const (
	OK ErrCode = iota
	ErrParam
	ErrInternal
)

type errSpecItem = struct {
	http int
	msg  string
}

var errSpec = map[ErrCode]errSpecItem{
	OK:          {http.StatusOK, "OK"},
	ErrParam:    {http.StatusBadRequest, "Bad Request"},
	ErrInternal: {http.StatusInternalServerError, "Internal Server Error"},
}

func (c BaseController) ResponseFailure(g *gin.Context, e ErrCode, detail string) {
	g.JSON(errSpec[e].http, gin.H{
		"code":   errSpec[e].http,
		"msg":    errSpec[e].msg,
		"detail": detail,
	})
	g.Abort() // 确保后续中间件/处理不再继续
}
