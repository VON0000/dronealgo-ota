package router

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/von0000/dronealgo-ota/platform/cmd/server/controller"
)

func SetRouters(r *gin.Engine) {
	r.GET("/swagger/*any",
		ginSwagger.WrapHandler(
			swaggerFiles.Handler,
			ginSwagger.InstanceName("swagger"),  // ← 关键
			ginSwagger.URL("/swagger/doc.json"), // 可选，显式指定文档地址
		),
	)
	v1 := r.Group("/api/v1")
	fileAPI := &controller.FileController{}
	{
		v1.POST("/publish", fileAPI.Publish)
		v1.GET("/check", fileAPI.Check)
		v1.GET("/download/:version", fileAPI.Download)
	}
}
