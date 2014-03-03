package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"strconv"

	"github.com/hoisie/web"
	"github.com/nfleet/via/geo"
)

var allowed_speeds = []int{40, 60, 80, 100, 120}

func contains(a int, list []int) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func check_coordinate_sanity(matrix []Coord, country string) (bool, error) {
	bbox := geo.BoundingBoxes[country]

	verify := func(pair []float64) bool {
		lat, long := pair[0], pair[1]
		if long > bbox["long_max"] || long < bbox["long_min"] || lat > bbox["lat_max"] || lat < bbox["lat_min"] {
			return false
		}
		return true
	}

	for i, pair := range matrix {
		res := verify(pair)
		if !res {
			return false, errors.New(fmt.Sprintf("Coordinate (lat: %f, long: %f) at matrix index %d is outside the limits for country \"%s\" which is %vi. Make sure you use [[LAT, LONG]...].", pair[0], pair[1], i, country, bbox))
		}
	}
	return true, nil
}

// Starts a computation, validates the matrix in POST.
// If matrix data is missing, returns 400 Bad Request.
// If on the other hand matrix is data is not missing,
// but makes no sense, it returns 422 Unprocessable Entity.
func (server *Server) PostMatrix(ctx *web.Context) {
	var hash string
	var computed bool
	params := []string{"matrix", "speed_profile", "country"}

	body := ctx.Request.Body

	var buf bytes.Buffer
	buf.ReadFrom(body)
	bodyParams := buf.Bytes()
	debug.Println(buf.String())
	var paramBlob map[string]interface{}
	// Parse params
	json.Unmarshal(bodyParams, &paramBlob)

	ok := false
	for _, param := range params {
		// ok will be set to false if ctx.Params doesn't contain param
		_, ok = paramBlob[param]
	}

	if ok {
		data := paramBlob["matrix"].(string)
		country := paramBlob["country"].(string)
		sp, jep := paramBlob["speed_profile"].(float64)

		speed_profile := int(sp)
		// Sanitize speed profile.
		if !jep || !contains(speed_profile, allowed_speeds) {
			msg := fmt.Sprintf("speed profile '%d' makes no sense, must be one of %s", speed_profile, fmt.Sprint(allowed_speeds))
			ctx.Abort(422, msg)
			return
		}
		// Sanitize country.
		if _, ok := server.AllowedCountries[country]; !ok {
			countries := ""
			for k := range server.AllowedCountries {
				countries += k + " "
			}
			ctx.Abort(422, "country "+country+" not allowed, must be one of: "+countries)
			return
		}

		mat, err := geo.ParseJsonMatrix(data)
		if err != nil {
			ctx.Abort(422, err.Error())
			return
		}

		ok, err = check_coordinate_sanity(mat, country)
		if err != nil {
			ctx.Abort(422, err.Error())
			return
		}

		hash, computed = server.CreateMatrixComputation(mat, country, int(speed_profile))

		// launch computation here if the result wasn't proxied.
		if !computed {
			go server.ComputeMatrix(hash)
		}
	} else {
		ctx.Abort(400, "Missing matrix data or speed profile or country. You sent: "+buf.String())
		return
	}

	loc := fmt.Sprintf("/spp/%s", hash)

	ctx.Redirect(201, loc)
}

// Returns a computation from the server as identified by the resource parameter
// in GET.
func (server *Server) GetMatrix(ctx *web.Context, resource string) string {
	progress, err := server.GetMatrixComputationProgress(resource)
	if err != nil {
		ctx.Abort(500, err.Error())
		return ""
	}

	if progress == "complete" {
		url := fmt.Sprintf("/spp/%s/result", resource)
		debug.Println("redirect ->", url)
		ctx.Redirect(303, url)
	} else {
		ctx.ContentType("json")
		msg := fmt.Sprintf(`{ "progress": "%s" }`, progress)
		return msg
	}

	return ""
}

func (server *Server) GetMatrixResult(ctx *web.Context, resource string) string {
	if ex, _ := server.client.Exists(resource); !ex {
		ctx.Abort(500, "Result expired. POST again.")
		return ""
	}

	progress, err := server.GetMatrixComputationProgress(resource)
	if progress != "complete" {
		ctx.Abort(403, "Computation is not ready yet.")
		return ""
	}

	if exists, _ := server.client.Hexists(resource, "see"); exists {
		debug.Println("Got proxy result")
		pointer, _ := server.client.Hget(resource, "see")
		resource = string(pointer)
	}

	data, err := server.client.Hget(resource, "result")
	if err != nil {
		ctx.Abort(500, "Redis error: "+err.Error())
		return ""
	}

	if data != nil {
		ctx.ContentType("json")
		return fmt.Sprintf("{ \"Matrix\": %s }", string(data))
	}

	return ""
}

func (server *Server) GetServerStatus(ctx *web.Context) string {
	db, _ := sql.Open("postgres", server.Config.String())
	defer db.Close()

	err := db.Ping()

	if err != nil {
		ctx.Abort(500, "Could not connect to database: "+err.Error())
		return ""
	}

	return "OK"
}

func (server *Server) GetCorrectCoordinate(ctx *web.Context) string {
	s_lat, lat_ok := ctx.Params["lat"]
	s_long, long_ok := ctx.Params["long"]

	// TODO(ane): implement automatic lookup of country
	s_country, country_ok := ctx.Params["country"]

	if !lat_ok || !long_ok || !country_ok {
		ctx.Abort(400, fmt.Sprintf("Missing parameter, need lat, long, country, you gave: %q", ctx.Params))
		return ""
	}

	lat, err := strconv.ParseFloat(s_lat, 32)
	if err != nil {
		ctx.Abort(400, fmt.Sprintf("Latitude %s is invalid, cannot parse!", s_lat))
		return ""
	}

	long, err := strconv.ParseFloat(s_long, 32)
	if err != nil {
		ctx.Abort(400, fmt.Sprintf("Longitude %s is invalid, cannot parse!", s_long))
		return ""
	}

	if _, ok := server.Config.AllowedCountries[s_country]; !ok {
		ctx.Abort(500, fmt.Sprintf("Country %s not allowed", s_country))
		return ""
	}

	coord := Coord{lat, long}

	corr_node, err := geo.CorrectPoint(server.Config, coord, s_country)
	if err != nil {
		ctx.Abort(500, err.Error())
		return ""
	}

	response, err := json.Marshal(corr_node)
	if err != nil {
		ctx.Abort(500, err.Error())
		return ""
	}

	ctx.Header().Set("Access-Control-Allow-Origin", "*")
	ctx.ContentType("application/json")
	return string(response)
}

func (server *Server) GetNodesToCoordinates(ctx *web.Context) string {
	nodes, ex := ctx.Params["nodes"]
	country, ex2 := ctx.Params["country"]

	if !ex || !ex2 {
		ctx.Abort(400, fmt.Sprintf("Missing parameter: either nodes or country is missing"))
		return ""
	}

	var parsedNodes []int
	if err := json.Unmarshal([]byte(nodes), &parsedNodes); err != nil {
		ctx.Abort(400, err.Error())
		return ""
	}

	coordinates, err := geo.GetCoordinates(server.Config, country, parsedNodes)
	if err != nil {
		ctx.Abort(500, err.Error())
		return ""
	}

	cont, err := json.Marshal(coordinates)
	if err != nil {
		ctx.Abort(500, err.Error())
		return ""
	}

	ctx.Header().Set("Access-Control-Allow-Origin", "*")
	ctx.ContentType("application/json")
	return string(cont)
}

func (server *Server) PostCoordinatePaths(ctx *web.Context) string {
	content, err := ioutil.ReadAll(ctx.Request.Body)

	var (
		paths    PathsInput
		computed []CoordinatePath
	)

	if err := json.Unmarshal(content, &paths); err != nil {
		ctx.Abort(400, "Couldn't parse JSON: "+err.Error()+" in '"+string(content)+"'")
		return ""
	} else {
		var err error
		computed, err = geo.CalculateCoordinatePaths(server.Config, paths)
		if err != nil {
			ctx.Abort(422, "Couldn't resolve addresses: "+err.Error())
			return ""
		}
	}

	res, err := json.Marshal(computed)
	if err != nil {
		ctx.Abort(500, "Couldn't serialize paths: "+err.Error())
		return ""
	}

	ctx.Header().Set("Access-Control-Allow-Origin", "*")
	ctx.ContentType("application/json")
	return string(res)
}

func (server *Server) PostResolve(ctx *web.Context) string {
	content, err := ioutil.ReadAll(ctx.Request.Body)
	var locations, resolvedLocations []Location

	// Parse params
	if err := json.Unmarshal(content, &locations); err != nil {
		ctx.Abort(400, "Couldn't parse JSON: "+err.Error())
		return ""
	} else {
		for i := 0; i < len(locations); i++ {
			newLoc, err := geo.ResolveLocation(server.Config, locations[i])
			if err != nil {
				ctx.Abort(422, "Resolvation failure: "+err.Error())
			}
			resolvedLocations = append(resolvedLocations, newLoc)
		}
	}

	res, err := json.Marshal(resolvedLocations)
	if err != nil {
		ctx.Abort(500, err.Error())
	}

	ctx.Header().Set("Access-Control-Allow-Origin", "*")
	ctx.ContentType("application/json")
	return string(res)
}