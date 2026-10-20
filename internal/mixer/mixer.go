// Package mixer owns station-wide music and presenter levels.
package mixer

import (
	"context"
	"errors"
)

type Levels struct {
	Music, DJ      int
	NormaliseMusic bool
}

func Default() Levels { return Levels{Music: 100, DJ: 100} }
func (v Levels) Validate() error {
	if v.Music < 0 || v.Music > 400 || v.DJ < 0 || v.DJ > 400 {
		return errors.New("Choose music and DJ volumes between 0% and 400%.")
	}
	return nil
}

type Repository interface {
	Get(context.Context, string) (Levels, error)
	Save(context.Context, string, Levels) error
}
