package handlers

import (
	"errors"

	"github.com/gin-gonic/gin"
)

func (h *LinkHandler) resolveSlug(c *gin.Context, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}

	for range 5 {
		slug, err := randomSlug(7)
		if err != nil {
			return "", err
		}
		exists, err := h.queries.SlugExists(c.Request.Context(), slug)
		if err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
	}

	return "", errors.New("slug attempts exhausted")
}
