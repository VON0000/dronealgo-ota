package router

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/von0000/dronealgo-ota/platform/cmd/server/controller"
)

func SetRouters(r *gin.Engine) {
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	v1 := r.Group("/api/v1")
	fileAPI := &controller.FileController{}
	{
		v1.POST("/publish", fileAPI.Publish)
	}
}
