package handlers

import db "github.com/ibrahimijai/sh.sh/internal/db/generated"

type DomainHandler struct {
	queries db.Querier
}

func NewDomainHandler(queries db.Querier) *DomainHandler {
	return &DomainHandler{queries: queries}
}
