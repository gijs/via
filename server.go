package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/hoisie/redis"
	"github.com/hoisie/web"

	// register this postgres driver with the SQL module
	_ "github.com/bmizerany/pq"
)

type (
	Server struct {
		client redis.Client
		Config
	}

	Config struct {
		Port             int
		DbUser           string
		DbName           string
		AllowedCountries map[string]bool
	}

	Coord     []float64
	debugging bool
)

const (
	expiry int64 = 3600
)

var (
	Debug    bool
	Parallel bool
	debug    debugging
)

func (config *Config) String() string {
	s := fmt.Sprintf("sslmode=disable user=%s dbname=%s", config.DbUser, config.DbName)
	return s
}

func (s *Server) Splash(ctx *web.Context) {
	ctx.ContentType("image/jpeg")
	http.ServeFile(ctx, ctx.Request, "./splash.jpg")
}

func load_config(file string) (Config, error) {
	contents, err := ioutil.ReadFile(file)
	if err != nil {
		return Config{}, err
	}

	var config Config
	if err := json.Unmarshal(contents, &config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func parse_flags() {
	flag.BoolVar(&Debug, "debug", false, "toggle debugging on/off")
	flag.BoolVar(&Parallel, "par", false, "turn on parallel execution")
	flag.Parse()
}

func test_connection() {
	// redis
	fmt.Printf("loading services")

}

func Options(ctx *web.Context, route string) string {
	ctx.SetHeader("Access-Control-Allow-Origin", "*", false)
	ctx.SetHeader("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match", false)
	ctx.SetHeader("Access-control-Allow-Methods", "GET, PUT, POST, DELETE", false)
	ctx.ContentType("application/json")
	return "{}"
}

func main() {
	parse_flags()

	var config Config
	var configFile string
	var redis redis.Client

	debug = debugging(Debug)

	args := flag.Args()
	if len(args) < 1 {
		configFile = "production.json"
	} else {
		configFile = args[0]
	}

	debug.Println("loading config from " + configFile)
	config, err := load_config(configFile)
	if err != nil {
		fmt.Printf("configuration loading from %s failed: %s\n", configFile, err.Error())
		return
	}

	server := Server{client: redis, Config: config}

	// Basic
	web.Get("/", server.Splash)
	web.Get("/status", server.GetServerStatus)

	// Dmatrix
	web.Get("/dm/(.*)/result", server.GetMatrixResult)
	web.Get("/dm/(.*)$", server.GetMatrix)
	web.Post("/dm/", server.PostMatrix)

	// Path
	web.Post("/paths", server.PostCoordinatePaths)

	// Address/Coordinate
	web.Post("/resolve", server.PostResolve)

	web.Match("OPTIONS", "/(.*)", Options)

	web.Run(fmt.Sprintf("127.0.0.1:%d", config.Port))
}
