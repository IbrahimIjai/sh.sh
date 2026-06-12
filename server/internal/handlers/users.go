package handlers

import (
	"net/http"
	"log"
	"github.com/gin-gonic/gin"
)

type CreateUserRequest struct {
	Name  string `json:"name" binding:"required"`
	Email string `json:"email" binding:"required,email"`
}

func CreateUser(c *gin.Context) {
	var req CreateUserRequest

	log.Printf("Received request to create user: %v", req)

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "user created successfully",
		"user": gin.H{
			"name":  req.Name,
			"email": req.Email,
		},
	})
}
