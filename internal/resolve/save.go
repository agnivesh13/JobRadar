package resolve

import (
	"context"
	"strings"

	"jobradar/internal/model"
	"jobradar/internal/store"
)

// Meta says where a resolved company came from.
type Meta struct {
	Source       string // deep_search | rss | hn | yc | search | outbound
	Detail       string // e.g. the headline
	FundingStage string
}

// Save stores what the resolver found: a new company, or an update to the
// one already known under that name or board. It reports whether the company
// is new to the list.
func Save(ctx context.Context, st *store.Store, res Result, m Meta) (model.Company, error) {
	c, _, err := SaveNew(ctx, st, res, m)
	return c, err
}

func SaveNew(ctx context.Context, st *store.Store, res Result, m Meta) (model.Company, bool, error) {
	c := model.Company{Name: DisplayName(res.Name), Domain: res.Domain, CareersURL: res.CareersURL, Source: m.Source, FundingStage: m.FundingStage}
	if res.Board != "" {
		c.BoardType, c.BoardToken = res.Board, res.BoardToken
	} else {
		c.BoardType = model.BoardCustom
	}
	id, created, err := st.AddCompany(ctx, c)
	if err != nil {
		return c, false, err
	}
	if created && m.Detail != "" {
		st.SetSourceDetail(ctx, id, m.Detail)
	}
	if !created {
		if m.FundingStage != "" {
			st.SetFundingStage(ctx, id, m.FundingStage)
		}
		if m.Source == "deep_search" {
			// The user asked for it: refresh how to crawl it and make sure it is active.
			active := "active"
			patch := store.CompanyPatch{BoardType: &c.BoardType, Status: &active}
			if c.BoardToken != "" {
				patch.BoardToken = &c.BoardToken
			}
			if c.CareersURL != "" {
				patch.CareersURL = &c.CareersURL
			}
			if c.Domain != "" {
				patch.Domain = &c.Domain
			}
			if err := st.UpdateCompany(ctx, id, patch); err != nil {
				return c, false, err
			}
		}
	}
	c, err = st.GetCompany(ctx, id)
	return c, created, err
}

// DisplayName tidies a typed name: "acme labs" -> "Acme Labs".
func DisplayName(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if w == strings.ToLower(w) && len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
