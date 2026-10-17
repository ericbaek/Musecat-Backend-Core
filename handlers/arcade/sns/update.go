package sns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/ericbaek/musecat-backend-core/service/xp"
)

// UpdateArcadeSNSBody represents the request body for updating arcade SNS.
type UpdateArcadeSNSBody struct {
	Arcade string         `json:"arcade"`
	SNS    []SNSAtomInput `json:"sns"`
}

// SNSAtomInput represents a single SNS atom to be saved under a SNS molecule.
type SNSAtomInput struct {
	source       *core.Record
	nativeFields map[string]bool
	Type         string `json:"type"`           // required
	Link         string `json:"link"`           // required
	Name         string `json:"name,omitempty"` // optional
}

func parseUpdateSNSBody(re *core.RequestEvent) (UpdateArcadeSNSBody, error) {
	var body UpdateArcadeSNSBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func validateUpdateSNSBody(body UpdateArcadeSNSBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	for i, a := range body.SNS {
		if a.Type == "" {
			return fmt.Errorf("sns[%d].type is required", i)
		}
		if a.Link == "" {
			return fmt.Errorf("sns[%d].link is required", i)
		}
		if arcadeinternal.IsPhoneSNSType(a.Type) && arcadeinternal.NormalizePhoneValue(a.Link) == "" {
			return fmt.Errorf("sns[%d].link must contain a valid phone number", i)
		}
		// name is optional
	}
	return nil
}

// UpdateArcadeSNS creates a new arcade_sns record (molecule), attaches atoms in arcade_sns_atoms,
// and updates the arcade.sns relation to the new molecule id. Uses best-effort rollback on failure.
func UpdateArcadeSNS(re *core.RequestEvent) error {
	body, err := parseUpdateSNSBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	return updateArcadeSNS(re, body, nil)
}

func updateArcadeSNS(re *core.RequestEvent, body UpdateArcadeSNSBody, patch *nativeSNSPatch) error {

	if err := validateUpdateSNSBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	// The response should mirror the GET /arcade?expand=sns shape, so we keep the
	// newly created molecule id plus the rendered atom list together here.
	var newSNSId string
	var expandedSNSValue ExpandedSNSValue
	var xpFeedback xp.ExpFeedback

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		// 1) Verify the arcade exists and capture the previous SNS molecule for diffing.
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		if !arcadeinternal.CanWriteArcade(re.Auth, arcadeRec) {
			return arcadeinternal.ErrArcadeWriteForbidden
		}
		if patch != nil && arcadeRec.GetString("sns") != *patch.Revision {
			return &arcadeinternal.NativeEditError{Status: 409, Code: "revision_conflict"}
		}
		prevAtoms, err := loadCurrentSNSAtoms(txApp, body.Arcade, strings.TrimSpace(arcadeRec.GetString("sns")))
		if err != nil {
			return err
		}
		if patch != nil {
			body.SNS, err = mergeNativeSNS(txApp, prevAtoms, *patch)
			if err != nil {
				return err
			}
		}
		baseExp, err := xp.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		// 2) Create the new SNS molecule that will replace arcade.sns.
		snsColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeSNS)
		if err != nil {
			return fmt.Errorf("failed to find arcade_sns: %w", err)
		}
		snsRec := core.NewRecord(snsColl)
		if patch != nil && *patch.Revision != "" {
			previous, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeSNS, *patch.Revision)
			if err != nil {
				return err
			}
			arcadeinternal.CopyRevisionContent(snsRec, previous)
		}
		snsRec.Set("arcade", body.Arcade)
		snsRec.Set("createdBy", re.Auth.Id)
		if err := txApp.Save(snsRec); err != nil {
			return fmt.Errorf("failed to create arcade_sns: %w", err)
		}

		newSNSId = snsRec.Id

		// 3) Create the SNS atoms under that molecule and keep a rendered copy
		//    for the success response.
		atomColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeSNSAtoms)
		if err != nil {
			return fmt.Errorf("failed to find arcade_sns_atoms: %w", err)
		}
		nextAtoms := make([]snsAtomState, 0, len(body.SNS))
		nextItems := make([]ExpandedSNSItem, 0, len(body.SNS))

		for i, a := range body.SNS {
			a = normalizeSNSInput(a)
			phoneValue := ""
			displayLink := a.Link
			if arcadeinternal.IsPhoneSNSType(a.Type) {
				phoneValue = arcadeinternal.NormalizePhoneValue(a.Link)
				displayLink = arcadeinternal.ResolveSNSLinkForOutput(a.Type, a.Link, phoneValue)
			}
			atom := core.NewRecord(atomColl)
			atom.Set("molecule", newSNSId)
			if a.source != nil {
				arcadeinternal.CopyRevisionContent(atom, a.source)
			} else {
				atom.Set("type", a.Type)
			}
			if a.source == nil || a.nativeFields["link"] {
				if arcadeinternal.IsPhoneSNSType(a.Type) {
					atom.Set("link", "")
					atom.Set("phone", phoneValue)
				} else {
					atom.Set("link", a.Link)
					atom.Set("phone", "")
				}
			}
			if a.source == nil || a.nativeFields["name"] {
				atom.Set("name", a.Name)
			}

			atom.Set("createdBy", re.Auth.Id)

			if err := txApp.Save(atom); err != nil {
				return fmt.Errorf("failed to create sns atom %d: %w", i, err)
			}
			nextAtoms = append(nextAtoms, snsAtomState{
				AtomID: atom.Id,
				Type:   a.Type,
				Link:   displayLink,
				Name:   a.Name,
			})
			nextItems = append(nextItems, ExpandedSNSItem{
				Type: a.Type,
				Link: displayLink,
				Name: a.Name,
			})
		}

		// 4) Compare old and new atoms so the changelog records added / updated / deleted entries.
		matches := matchSNSAtoms(prevAtoms, nextAtoms)
		if patch != nil {
			matches = map[int]int{}
			for nextIdx, a := range body.SNS {
				if a.source != nil {
					for prevIdx, prev := range prevAtoms {
						if prev.AtomID == a.source.Id {
							matches[nextIdx] = prevIdx
							break
						}
					}
				}
			}
		}
		snsLogItems := make([]snsDiffLogItem, 0, len(nextAtoms)+len(prevAtoms))
		referencedPrev := map[int]struct{}{}
		for nextIdx, nextAtom := range nextAtoms {
			if prevIdx, ok := matches[nextIdx]; ok {
				referencedPrev[prevIdx] = struct{}{}
				prevAtom := prevAtoms[prevIdx]
				snsLogItems = append(snsLogItems, buildSNSDiffLogItem(nextAtom, &prevAtom))
				continue
			}
			snsLogItems = append(snsLogItems, buildSNSDiffLogItem(nextAtom, nil))
		}
		for prevIdx, prevAtom := range prevAtoms {
			if _, ok := referencedPrev[prevIdx]; ok {
				continue
			}
			snsLogItems = append(snsLogItems, buildDeletedSNSDiffLogItem(prevAtom))
		}

		// 5) Update arcade.sns to point at the new molecule and write the changelog envelope.
		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			body.Arcade,
			map[string]any{"sns": newSNSId},
			map[string]any{"sns": arcadeinternal.BuildChangelogEnvelope("sns", snsLogItems)},
			re.Auth.Id,
		); err != nil {
			return fmt.Errorf("failed to update arcade.sns: %w", err)
		}
		if arcadeRec.GetBool("public") {
			nextExp, _, err := xp.AwardArcadeEditExpTx(txApp, re.Auth.Id, body.Arcade, "sns", 2, baseExp, time.Now().UTC())
			if err != nil {
				return err
			}
			currentExp = nextExp
		}

		expandedSNSValue = BuildExpandedSNSValue(newSNSId, nextItems)
		xpFeedback = xp.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		var nativeErr *arcadeinternal.NativeEditError
		if errors.As(err, &nativeErr) {
			return re.JSON(nativeErr.Status, map[string]string{"error": nativeErr.Code})
		}
		if errors.Is(err, arcadeinternal.ErrArcadeWriteForbidden) {
			return re.JSON(http.StatusForbidden, map[string]any{"error": err.Error()})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "transaction failed",
			"details": err.Error(),
		})
	}

	if patch != nil {
		return re.JSON(http.StatusOK, map[string]any{"arcade": body.Arcade, "revision": newSNSId, "xp_feedback": xpFeedback})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":      body.Arcade,
		"sns":         expandedSNSValue,
		"xp_feedback": xpFeedback,
	})
}
