package game

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

type PriceItem struct {
	Title     *string  `json:"title,omitempty"`
	Value     *float32 `json:"value"`
	ModeKey   *string  `json:"mode_key,omitempty"`
	Represent *bool    `json:"represent,omitempty"`
}

type Price struct {
	Currency string      `json:"currency"`
	Type     string      `json:"type"`
	List     []PriceItem `json:"list"`
	Accept   []string    `json:"accept"`
}

type GameAtomInput struct {
	Game      string    `json:"game"`
	PrevID    string    `json:"prev_id,omitempty"`
	Location  string    `json:"location"`
	Quantity  int       `json:"quantity"`
	Price     Price     `json:"price"`
	Tag       []TagItem `json:"tag"`
	Uncertain bool      `json:"uncertain,omitempty"`
	PrevGame  string    `json:"prev_game,omitempty"`
	RawPrice  any       `json:"-"`
	RawTag    any       `json:"-"`
}

type UpdateArcadeGameBody struct {
	Arcade string          `json:"arcade"`
	Games  []GameAtomInput `json:"games"`
}

func parseUpdateGameBody(re *core.RequestEvent) (UpdateArcadeGameBody, error) {
	var body UpdateArcadeGameBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func NormalizePriceForRead(p Price) Price {
	p.Currency = strings.TrimSpace(p.Currency)
	priceType := strings.TrimSpace(p.Type)
	if err := ValidatePriceType(priceType); err != nil {
		p.Type = string(PriceTypeCustom)
	} else {
		p.Type = priceType
	}
	if p.List == nil {
		p.List = []PriceItem{}
	}
	if p.Accept == nil {
		p.Accept = []string{}
	}
	return p
}

func NormalizePriceForStorage(p Price) Price {
	p.Currency = strings.TrimSpace(p.Currency)
	p.Type = strings.TrimSpace(p.Type)
	for i := range p.List {
		if p.List[i].Title != nil {
			title := strings.TrimSpace(*p.List[i].Title)
			p.List[i].Title = &title
		}
		if p.List[i].ModeKey != nil {
			modeKey := strings.TrimSpace(*p.List[i].ModeKey)
			p.List[i].ModeKey = &modeKey
		}
	}

	if p.Accept == nil {
		p.Accept = []string{}
	} else {
		for i := range p.Accept {
			p.Accept[i] = strings.TrimSpace(p.Accept[i])
		}
	}
	return p
}

func NormalizeTagForStorage(tags any) any {
	return arcadeinternal.NormalizeGameTagPayload(tags)
}

func validatePrice(p Price) error {
	if strings.TrimSpace(p.Currency) == "" {
		return fmt.Errorf("price.currency is required")
	}

	if strings.TrimSpace(p.Type) == "" {
		return fmt.Errorf("price.type is required")
	}
	if err := ValidatePriceType(p.Type); err != nil {
		return err
	}

	// Current policy keeps list required for every type, including "free".
	if len(p.List) == 0 {
		return fmt.Errorf("price.list must have at least 1 item")
	}
	for i, it := range p.List {
		if it.Value != nil && *it.Value <= 0 {
			return fmt.Errorf("price.list[%d].value must be > 0 or null", i)
		}
		// title optional
	}
	if err := ValidatePriceAccept(p.Accept); err != nil {
		return err
	}
	return nil
}

func validateUpdateGameBody(body *UpdateArcadeGameBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	for i := range body.Games {
		g := &body.Games[i]
		if g.Game == "" {
			return fmt.Errorf("games[%d].game is required", i)
		}
		if g.Quantity <= 0 {
			return fmt.Errorf("games[%d].quantity must be > 0", i)
		}
		if err := validatePrice(g.Price); err != nil {
			return fmt.Errorf("games[%d].%v", i, err)
		}
		if err := ValidateTagItems(g.Tag); err != nil {
			return fmt.Errorf("games[%d].%v", i, err)
		}
	}
	return nil
}

func updateArcadeGameTx(txApp core.App, body UpdateArcadeGameBody, createdBy string, validate bool, action string) (string, error) {
	if validate {
		if err := validateUpdateGameBody(&body); err != nil {
			return "", err
		}
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		return "", fmt.Errorf("createdBy is required")
	}

	arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
	if err != nil {
		return "", fmt.Errorf("arcade not found: %w", err)
	}
	oldMoleculeID := strings.TrimSpace(arcadeRec.GetString("game"))

	prevCurrentAtoms := make([]*core.Record, 0)
	prevCurrentAtomsByID := map[string]*core.Record{}
	if oldMoleculeID != "" {
		prevCurrentAtoms, err = txApp.FindRecordsByFilter(
			arcadeinternal.CollectionArcadeGameAtoms,
			"molecule={:id}",
			"+created",
			0,
			0,
			dbx.Params{"id": oldMoleculeID},
		)
		if err != nil {
			return "", fmt.Errorf("failed to load previous game atoms: %w", err)
		}
		for _, atom := range prevCurrentAtoms {
			prevCurrentAtomsByID[atom.Id] = atom
		}
	}

	gameColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGame)
	if err != nil {
		return "", fmt.Errorf("failed to find arcade_game: %w", err)
	}
	mol := core.NewRecord(gameColl)
	mol.Set("arcade", body.Arcade)
	mol.Set("createdBy", createdBy)
	if err := txApp.Save(mol); err != nil {
		return "", fmt.Errorf("failed to create arcade_game: %w", err)
	}
	newMoleculeID := mol.Id

	atomColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGameAtoms)
	if err != nil {
		return "", fmt.Errorf("failed to find arcade_game_atoms: %w", err)
	}
	gameLogItems := make([]gameDiffLogItem, 0, len(body.Games))
	referencedCurrentPrevIDs := map[string]struct{}{}
	for i, g := range body.Games {
		inheritedFlags := []string{}
		var prevAtom *core.Record
		if prevID := strings.TrimSpace(g.PrevID); prevID != "" {
			prevAtom, err = txApp.FindRecordById(arcadeinternal.CollectionArcadeGameAtoms, prevID)
			if err != nil {
				return "", fmt.Errorf("games[%d].prev_id not found: %w", i, err)
			}
			prevMoleculeID := prevAtom.GetString("molecule")
			if prevMoleculeID == "" {
				return "", fmt.Errorf("games[%d].prev_id has no molecule", i)
			}
			prevMolecule, err := txApp.FindRecordById(arcadeinternal.CollectionArcadeGame, prevMoleculeID)
			if err != nil {
				return "", fmt.Errorf("games[%d].prev_id molecule not found: %w", i, err)
			}
			if prevMolecule.GetString("arcade") != body.Arcade {
				return "", fmt.Errorf("games[%d].prev_id does not belong to arcade", i)
			}
			inheritedFlags = prevAtom.GetStringSlice("flags")
			if _, ok := prevCurrentAtomsByID[prevID]; ok {
				referencedCurrentPrevIDs[prevID] = struct{}{}
			}
		}

		atom := core.NewRecord(atomColl)
		atom.Set("molecule", newMoleculeID)
		atom.Set("game", g.Game)
		atom.Set("location", g.Location)
		atom.Set("quantity", g.Quantity)
		atom.Set("flags", inheritedFlags)
		if g.RawPrice != nil {
			atom.Set("price", g.RawPrice)
		} else {
			atom.Set("price", NormalizePriceForStorage(g.Price))
		}
		if g.RawTag != nil {
			atom.Set("tag", NormalizeTagForStorage(g.RawTag))
		} else {
			atom.Set("tag", NormalizeTagForStorage(g.Tag))
		}
		atom.Set("uncertain", g.Uncertain)
		if prevGame := strings.TrimSpace(g.PrevGame); prevGame != "" {
			atom.Set("prev_game", prevGame)
		} else {
			atom.Set("prev_game", nil)
		}
		atom.Set("createdBy", createdBy)
		if err := txApp.Save(atom); err != nil {
			return "", fmt.Errorf("failed to create game atom %d: %w", i, err)
		}
		gameLogItems = append(gameLogItems, buildGameDiffLogItem(atom.Id, g, prevAtom, action))
	}
	for _, prevAtom := range prevCurrentAtoms {
		if _, ok := referencedCurrentPrevIDs[prevAtom.Id]; ok {
			continue
		}
		gameLogItems = append(gameLogItems, buildDeletedGameDiffLogItem(prevAtom))
	}

	gameChangeLog := arcadeinternal.BuildChangelogEnvelope("game", gameLogItems)
	if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
		txApp,
		arcadeRec.Id,
		map[string]any{"game": newMoleculeID},
		map[string]any{"game": gameChangeLog},
		createdBy,
	); err != nil {
		return "", err
	}

	return newMoleculeID, nil
}

// UpdateArcadeGameTx creates a new arcade_game record, its atoms, and updates arcade.game
// inside the provided transaction, validating the supplied body.
func UpdateArcadeGameTx(txApp core.App, body UpdateArcadeGameBody, createdBy string) (string, error) {
	return updateArcadeGameTx(txApp, body, createdBy, true, "")
}

// UpdateArcadeGameTxFromExistingAtoms clones an existing game version into a new one without
// re-validating the copied atom fields.
func UpdateArcadeGameTxFromExistingAtoms(txApp core.App, body UpdateArcadeGameBody, createdBy string, action string) (string, error) {
	return updateArcadeGameTx(txApp, body, createdBy, false, action)
}

// UpdateArcadeGame creates a new arcade_game record, its atoms, and updates arcade.game atomically.
func UpdateArcadeGame(re *core.RequestEvent) error {
	body, err := parseUpdateGameBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateUpdateGameBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var newMoleculeID string
	var expandedGameValue map[string]any
	var xpFeedback userhandler.ExpFeedback
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp
		createdBy := ""
		if re.Auth != nil {
			createdBy = re.Auth.Id
		}
		newMoleculeID, err = UpdateArcadeGameTx(txApp, body, createdBy)
		if err != nil {
			return err
		}
		if arcadeRec.GetBool("public") {
			nextExp, _, err := userhandler.AwardArcadeEditExpTx(txApp, re.Auth.Id, body.Arcade, "game", 3, baseExp, time.Now().UTC())
			if err != nil {
				return err
			}
			currentExp = nextExp
		}
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "transaction failed",
			"details": err.Error(),
		})
	}

	if gameObj, ok := arcadeinternal.BuildExpandedGameValue(re.App, newMoleculeID); ok {
		expandedGameValue = gameObj
	} else {
		expandedGameValue = map[string]any{
			"id":    newMoleculeID,
			"items": []map[string]any{},
		}
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":      body.Arcade,
		"game":        expandedGameValue,
		"count":       len(body.Games),
		"xp_feedback": xpFeedback,
	})
}
