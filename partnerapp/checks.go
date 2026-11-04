package partnerapp

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var (
	ErrChecksBusy       = errors.New("checks already queued or rate limited")
	errCheckNetwork     = errors.New("network_or_address_blocked")
	errCheckResponse    = errors.New("invalid_response")
	errCheckCredentials = errors.New("credentials_unavailable")
)

const CheckSpec = "musecat-conformance/v1"

type CheckCase struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   string `json:"code"`
}

type CheckReport struct {
	ID       string      `json:"id"`
	Spec     string      `json:"spec"`
	Status   string      `json:"status"`
	Current  bool        `json:"current"`
	Created  string      `json:"created"`
	Started  string      `json:"started,omitempty"`
	Finished string      `json:"finished,omitempty"`
	Cases    []CheckCase `json:"cases"`
}

// Fixtures must be disposable staging credentials. They are sealed in the queue,
// wiped after processing, and excluded from reports, audit events and logs.
type CheckFixtures struct {
	AccessToken     string `json:"access_token,omitempty"`
	PrivateToken    string `json:"private_access_token,omitempty"`
	RefreshToken    string `json:"refresh_token,omitempty"`
	ExpectedSubject string `json:"expected_subject,omitempty"`
	PrivateSubject  string `json:"private_subject,omitempty"`
	FreshIntent     string `json:"fresh_intent,omitempty"`
	ExpiredIntent   string `json:"expired_intent,omitempty"`
	ConsumedIntent  string `json:"consumed_intent,omitempty"`
	JointAttemptID  string `json:"joint_attempt_id,omitempty"`
	Destructive     bool   `json:"destructive,omitempty"`
}

func checkRevision(r *core.Record) string {
	return hashSecret(r.GetString("submitted_form") + "\n" + r.GetString("direction_b_credentials") + "\n" + r.GetString("joint_credentials") + "\n" + r.GetString("secret_hash") + "\n" + r.GetString("partner_key_hash"))
}

func checkProjection(r, application *core.Record) CheckReport {
	var cases []CheckCase
	_ = r.UnmarshalJSONField("cases", &cases)
	if cases == nil {
		cases = []CheckCase{}
	}
	return CheckReport{ID: r.Id, Spec: CheckSpec, Status: r.GetString("status"), Current: r.GetString("revision") == checkRevision(application) && r.GetDateTime("created").Time().After(time.Now().Add(-30*24*time.Hour)), Created: r.GetString("created"), Started: r.GetString("started"), Finished: r.GetString("finished"), Cases: cases}
}

func (s *Service) CheckReports(app core.App, actorID, id string, operator bool) ([]CheckReport, error) {
	application, err := record(app, id)
	if err != nil {
		return nil, err
	}
	if !operator && application.GetString("owner") != actorID {
		return nil, ErrNotFound
	}
	rows, err := app.FindRecordsByFilter(CheckRuns, "application={:id}", "-created,-id", 10, 0, dbx.Params{"id": id})
	if err != nil {
		return nil, err
	}
	out := []CheckReport{}
	for _, r := range rows {
		out = append(out, checkProjection(r, application))
	}
	return out, nil
}

func (s *Service) QueueChecks(app core.App, actorID, id string, operator bool, fixtures CheckFixtures) (CheckReport, error) {
	if s.manager == nil {
		return CheckReport{}, ErrUnavailable
	}
	var out CheckReport
	err := app.RunInTransaction(func(tx core.App) error {
		application, err := record(tx, id)
		if err != nil {
			return err
		}
		if !operator && application.GetString("owner") != actorID {
			return ErrNotFound
		}
		if application.GetString("status") == StatusSuspended {
			return ErrState
		}
		form := formOf(application, "submitted_form")
		if form == nil {
			return ErrState
		}
		if err = form.Validate(); err != nil {
			return err
		}
		for _, v := range []string{fixtures.AccessToken, fixtures.PrivateToken, fixtures.RefreshToken, fixtures.FreshIntent, fixtures.ExpiredIntent, fixtures.ConsumedIntent} {
			if len(v) > 4096 || strings.ContainsAny(v, "\r\n") {
				return FieldErrors{"fixtures": "invalid"}
			}
		}
		if len(fixtures.ExpectedSubject) > 256 || len(fixtures.PrivateSubject) > 256 || (fixtures.JointAttemptID != "" && len(fixtures.JointAttemptID) != 15) {
			return FieldErrors{"fixtures": "invalid"}
		}
		if fixtures.RefreshToken != "" && !fixtures.Destructive {
			return FieldErrors{"destructive": "required"}
		}
		active, err := tx.CountRecords(CheckRuns, dbx.NewExp("actor={:actor} AND (status='queued' OR status='running')", dbx.Params{"actor": actorID}))
		if err != nil {
			return err
		}
		if active >= 3 {
			return ErrChecksBusy
		}
		rows, err := tx.FindRecordsByFilter(CheckRuns, "application={:id}", "-created", 1, 0, dbx.Params{"id": id})
		if err != nil {
			return err
		}
		if len(rows) > 0 && (slices.Contains([]string{"queued", "running"}, rows[0].GetString("status")) || rows[0].GetDateTime("created").Time().After(time.Now().Add(-time.Minute))) {
			return ErrChecksBusy
		}
		c, err := tx.FindCollectionByNameOrId(CheckRuns)
		if err != nil {
			return err
		}
		r := core.NewRecord(c)
		r.Set("application", id)
		r.Set("actor", actorID)
		r.Set("revision", checkRevision(application))
		r.Set("form", form)
		r.Set("status", "queued")
		r.Set("cases", []CheckCase{})
		if err = tx.Save(r); err != nil {
			return err
		}
		if fixtures != (CheckFixtures{}) {
			if s.connections == nil {
				return ErrNoConnectionKey
			}
			sealed, err := s.connections.Seal(fixtures, "conformance:"+r.Id)
			if err != nil {
				return err
			}
			r.Set("fixtures", sealed)
			if err = tx.Save(r); err != nil {
				return err
			}
		}
		out = checkProjection(r, application)
		return nil
	})
	return out, err
}

// ProcessChecks claims persisted work. No outbound request runs in a transaction.
// Runs interrupted by a process exit become failed; one-use fixtures are never retried.
func (s *Service) ProcessChecks(ctx context.Context, app core.App, limit int, runner *CheckRunner) error {
	if runner == nil {
		runner = NewCheckRunner(nil)
	}
	limit = max(1, min(limit, 4))
	rows, err := app.FindRecordsByFilter(CheckRuns, "status='queued' || (status='running' && started < {:cutoff})", "created", limit, 0, dbx.Params{"cutoff": time.Now().Add(-3 * time.Minute).UTC().Format("2006-01-02 15:04:05.000Z")})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.processCheck(ctx, app, row.Id, runner); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) processCheck(ctx context.Context, app core.App, id string, runner *CheckRunner) error {
	var run, application *core.Record
	var form *Form
	var fixtures CheckFixtures
	var creds Credentials
	var key string
	claimed := false
	err := app.RunInTransaction(func(tx core.App) error {
		var err error
		run, err = tx.FindRecordById(CheckRuns, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		application, err = record(tx, run.GetString("application"))
		if err != nil {
			return err
		}
		if run.GetString("status") == "running" {
			if run.GetDateTime("started").Time().After(time.Now().Add(-3 * time.Minute)) {
				return nil
			}
			run.Set("status", "failed")
			run.Set("cases", []CheckCase{{ID: "runner", Status: "fail", Code: "interrupted"}})
			run.Set("finished", time.Now().UTC())
			run.Set("fixtures", "")
			return tx.Save(run)
		}
		if run.GetString("status") != "queued" {
			return nil
		}
		actor, err := tx.FindRecordById("user", run.GetString("actor"))
		if err != nil {
			return err
		}
		if run.GetString("revision") != checkRevision(application) || application.GetString("status") == StatusSuspended || s.manager.CheckUser(tx, actor) != nil || (actor.Id != application.GetString("owner") && !slices.Contains(actor.GetStringSlice("tags"), "developer") && !slices.Contains(actor.GetStringSlice("tags"), "moderator")) {
			run.Set("status", "cancelled")
			run.Set("finished", time.Now().UTC())
			run.Set("fixtures", "")
			return tx.Save(run)
		}
		form = &Form{}
		if err = run.UnmarshalJSONField("form", form); err != nil {
			return err
		}
		if encrypted := run.GetString("fixtures"); encrypted != "" {
			if s.connections == nil {
				return ErrNoConnectionKey
			}
			if err = s.connections.Open(encrypted, "conformance:"+id, &fixtures); err != nil {
				return errCheckCredentials
			}
		}
		if s.connections != nil {
			if raw := application.GetString("direction_b_credentials"); raw != "" {
				if err = s.connections.Open(raw, credentialPurpose(application.Id), &creds); err != nil {
					return errCheckCredentials
				}
			}
			if raw := application.GetString("joint_credentials"); raw != "" {
				var keys jointCredentials
				if err = s.connections.Open(raw, jointPurpose(application.Id), &keys); err != nil {
					return errCheckCredentials
				}
				key = keys.OutboundKey
			}
		}
		run.Set("status", "running")
		run.Set("started", time.Now().UTC())
		if err = tx.Save(run); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if errors.Is(err, errCheckCredentials) {
		return app.RunInTransaction(func(tx core.App) error {
			r, e := tx.FindRecordById(CheckRuns, id)
			if e != nil {
				return e
			}
			if r.GetString("status") != "queued" {
				return nil
			}
			r.Set("status", "failed")
			r.Set("cases", []CheckCase{{ID: "runner", Status: "fail", Code: "credentials_unavailable"}})
			r.Set("finished", time.Now().UTC())
			r.Set("fixtures", "")
			return tx.Save(r)
		})
	}
	if err != nil || !claimed {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cases := runner.Run(bounded, *form, s.redirectURI(form.PartnerID), creds, key, fixtures)
	signIn := CheckCase{ID: "oauth.signin_roundtrip", Status: "action_required", Code: "signin_test_connection_required"}
	directionA := CheckCase{ID: "oauth.direction_a_roundtrip", Status: "action_required", Code: "direction_a_test_connection_required"}
	if len(form.DirectionA.RedirectURIs) > 0 && formHash(formOf(application, "approved_form")) == formHash(form) {
		grant, e := app.FindFirstRecordByFilter(delegation.Grants, "user={:owner} && client={:client}", dbx.Params{"owner": application.GetString("owner"), "client": form.PartnerID})
		if e == nil && form.DirectionA.LoginEnabled && s.manager.VerifyLoginGrant(app, application.GetString("owner"), form.PartnerID, grant.Id, grant.GetString("generation")) == nil {
			signIn.Status = "pass"
			signIn.Code = "signin_grant_verified"
			directionA.Status = "pass"
			directionA.Code = "direction_a_grant_verified"
		}
		if e == nil && s.manager.VerifyAccountGrant(app, application.GetString("owner"), form.PartnerID, grant.Id, grant.GetString("generation")) == nil {
			directionA.Status = "pass"
			directionA.Code = "direction_a_grant_verified"
		}
	}
	if len(form.DirectionA.RedirectURIs) == 0 {
		directionA.Status, directionA.Code = "not_applicable", "not_declared"
	}
	cases = append(cases, directionA)
	if form.DirectionA.LoginEnabled {
		cases = append(cases, signIn)
	}

	if form.Joint != nil && form.Joint.Enabled {
		result := CheckCase{ID: "joint.roundtrip", Status: "action_required", Code: "joint_test_connection_required"}
		if fixtures.JointAttemptID != "" && s.joint != nil {
			attempt, e := app.FindRecordById(connections.JointAttempts, fixtures.JointAttemptID)
			if e == nil && attempt.GetString("user") == application.GetString("owner") && attempt.GetString("partner") == form.PartnerID && (fixtures.ExpectedSubject == "" || attempt.GetString("partner_subject") == fixtures.ExpectedSubject) && formHash(formOf(application, "approved_form")) == formHash(form) {
				status, e := s.joint.PartnerStatus(app, form.PartnerID, attempt.GetString("correlation"))
				if e == nil && status.Status == "connected" && status.MusecatAccess && status.PartnerAccess {
					result.Status = "pass"
					result.Code = "verified_both_grants"
				} else {
					result.Status = "fail"
					result.Code = "joint_connection_not_live"
				}
			} else {
				result.Status = "fail"
				result.Code = "invalid_test_connection"
			}
		}
		cases = append(cases, result)
	}
	status := "passed"
	for _, c := range cases {
		if c.Status == "action_required" && status != "failed" {
			status = "incomplete"
		}
		if c.Status == "fail" {
			status = "failed"
		}
	}
	return app.RunInTransaction(func(tx core.App) error {
		current, err := tx.FindRecordById(CheckRuns, id)
		if err != nil {
			return err
		}
		if current.GetString("status") != "running" {
			return nil
		}
		current.Set("status", status)
		current.Set("cases", cases)
		current.Set("finished", time.Now().UTC())
		current.Set("fixtures", "")
		return tx.Save(current)
	})
}

func (s *Service) RegisterCheckCron(app core.App) {
	app.Cron().MustAdd("partner_conformance_checks", "* * * * *", func() {
		if err := s.ProcessChecks(context.Background(), app, 2, nil); err != nil {
			app.Logger().Error("Partner conformance worker failed")
		}
	})
}
