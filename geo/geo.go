package geo

import (
	"encoding/json"
	"io/ioutil"

	_ "github.com/bmizerany/pq"
	"github.com/hoisie/redis"
	"github.com/nfleet/via/geotypes"
)

type debugging bool

type Geo struct {
	Debug  debugging
	Expiry int
	Client redis.Client
	DB     geotypes.GeoDB
}

func LoadConfig(file string) (geotypes.Config, error) {
	contents, err := ioutil.ReadFile(file)
	if err != nil {
		return geotypes.Config{}, err
	}

	var config geotypes.Config
	if err := json.Unmarshal(contents, &config); err != nil {
		return geotypes.Config{}, err
	}
	return config, nil
}

func NewGeo(debug bool, db geotypes.GeoDB) *Geo {
	g := new(Geo)
	g.DB = db
	g.Debug = debugging(debug)
	return g
}
