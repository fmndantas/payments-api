package main

import (
	"github.com/gin-gonic/gin"

	"github.com/fmndantas/payments/internal/controller"
	"github.com/fmndantas/payments/internal/dependencies"
)

/*
type album struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Artist string  `json:"artist"`
	Price  float64 `json:"price"`
}

var albums []album

func getAlbums(c *gin.Context) {
	c.JSON(http.StatusOK, albums)
}

func postAlbums(c *gin.Context) {
	var newAlbum album

	if err := c.BindJSON(&newAlbum); err != nil {
		return
	}

	albums = append(albums, newAlbum)
	c.JSON(http.StatusCreated, newAlbum)
}

func getAlbumById(c *gin.Context) {
	id := c.Param("id")
	for _, a := range albums {
		if a.ID == id {
			c.JSON(http.StatusOK, a)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"message": "album not found"})
}
*/

func main() {
	/*
		albums = []album{
			{ID: "1", Title: "Blue Train", Artist: "Jonh Coltrane", Price: 56.99},
			{ID: "2", Title: "Jeru", Artist: "Gerry Mulligan", Price: 17.99},
		}
		router.GET("/albums", getAlbums)
		router.GET("/albums/:id", getAlbumById)
		router.POST("/albums", postAlbums)
	*/

	// FIXME: make this a env variable
	tree := dependencies.InitializeDefault("postgres://postgres:postgres@localhost:5432/payments")

	router := gin.Default()
	router.GET("health", tree.InjectToController(controller.CheckHealth))
	router.POST("checkout", tree.InjectToController(controller.Checkout))

	router.Run("localhost:8080")
}
