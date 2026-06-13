package handlers

import db "github.com/ibrahimijai/sh.sh/internal/db/generated"

type LinkHandler struct {
	queries db.Querier
}

func NewLinkHandler(queries db.Querier) *LinkHandler {
	return &LinkHandler{queries: queries}
}
