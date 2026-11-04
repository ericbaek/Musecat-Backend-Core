// Package contribution carries server-verified attribution through domain transactions.
package contribution

import (
	"context"
	"github.com/pocketbase/pocketbase/core"
)

const Field = "contribution_source"

// Source is an immutable public snapshot, never a credential or a caller-supplied claim.
type Source struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type attributedApp struct {
	core.App
	source Source
}

func WithSource(app core.App, source Source) core.App {
	return &attributedApp{App: app, source: source}
}

func FromApp(app core.App) *Source {
	if scoped, ok := app.(*attributedApp); ok {
		source := scoped.source
		return &source
	}
	return nil
}

func IsExternal(app core.App) bool { return FromApp(app) != nil }

func FromRecord(record *core.Record) *Source {
	var source Source
	if record.UnmarshalJSONField(Field, &source) != nil || source.Kind == "" {
		return nil
	}
	return &source
}

func (app *attributedApp) stamp(model core.Model) {
	record, ok := model.(*core.Record)
	if !ok || !record.IsNew() {
		return
	}
	switch record.Collection().Name {
	case "arcade", "arcade_changelog", "arcade_photo_atoms":
		record.Set(Field, app.source)
	}
}

func (app *attributedApp) Save(model core.Model) error {
	app.stamp(model)
	return app.App.Save(model)
}

func (app *attributedApp) SaveWithContext(ctx context.Context, model core.Model) error {
	app.stamp(model)
	return app.App.SaveWithContext(ctx, model)
}

func (app *attributedApp) RunInTransaction(fn func(core.App) error) error {
	return app.App.RunInTransaction(func(tx core.App) error { return fn(WithSource(tx, app.source)) })
}

// WithoutSource is used only when calculating deferred rewards from persisted origins.
func WithoutSource(app core.App) core.App {
	if scoped, ok := app.(*attributedApp); ok {
		return scoped.App
	}
	return app
}
