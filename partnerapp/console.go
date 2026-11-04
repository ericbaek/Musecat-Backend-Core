package partnerapp

import (
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"time"
)

type UsageDay struct {
	Day       string `json:"day" db:"day"`
	API       int    `json:"api" db:"api"`
	Codes     int    `json:"codes" db:"codes"`
	Refreshes int    `json:"refreshes" db:"refreshes"`
	Errors    int    `json:"errors" db:"errors"`
}
type Console struct {
	Issuer         string                        `json:"issuer"`
	Scopes         []string                      `json:"scopes"`
	RedirectURIs   []string                      `json:"redirect_uris"`
	ConnectedCount int                           `json:"connected_count"`
	Accounts       []delegation.ConnectedAccount `json:"accounts"`
	Page           int                           `json:"page"`
	Usage          []UsageDay                    `json:"usage"`
}

func (s *Service) Console(app core.App, ownerID, id string, page int) (Console, error) {
	out := Console{Scopes: []string{}, RedirectURIs: []string{}, Accounts: []delegation.ConnectedAccount{}, Usage: []UsageDay{}, Page: page}
	row, err := owned(app, ownerID, id)
	if err != nil {
		return out, err
	}
	if s.manager == nil {
		return out, ErrUnavailable
	}
	out.Issuer = s.manager.Issuer()
	if client, ok := s.manager.Client(row.GetString("partner_id")); ok {
		out.Scopes = client.Scopes
		out.RedirectURIs = client.RedirectURIs
	}
	out.Accounts, out.ConnectedCount, err = s.manager.ConnectedAccounts(app, row.GetString("partner_id"), page)
	if err != nil {
		return out, err
	}
	start := time.Now().UTC().AddDate(0, 0, -13)
	var days []UsageDay
	err = app.DB().NewQuery(`SELECT day,sum(CASE WHEN kind='api' THEN requests ELSE 0 END) AS api,sum(CASE WHEN kind='code' THEN requests ELSE 0 END) AS codes,sum(CASE WHEN kind='refresh' THEN requests ELSE 0 END) AS refreshes,sum(errors) AS errors FROM oauth_client_usage WHERE client={:client} AND day>={:day} GROUP BY day ORDER BY day`).Bind(dbx.Params{"client": row.GetString("partner_id"), "day": start.Format("2006-01-02")}).All(&days)
	if err != nil {
		return out, err
	}
	for i := 0; i < 14; i++ {
		day := UsageDay{Day: start.AddDate(0, 0, i).Format("2006-01-02")}
		for _, saved := range days {
			if saved.Day == day.Day {
				day = saved
				break
			}
		}
		out.Usage = append(out.Usage, day)
	}
	return out, nil
}
