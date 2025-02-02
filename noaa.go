// Package noaa implements a basic wrapper around api.weather.gov to
// grab HTTP responses to endpoints (i.e.: weather & forecast data)
// by the National Weather Service, an agency of the United States.
package noaa

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

// deprecated
// Default values for the weather.gov REST API config which will
// be replaced by Config. These are subject to deletion in the future.
// Instead, use noaa.GetConfig followed by:
//
//	Config.BaseURL, Config.UserAgent, Config.Accept
const (
	API       = "https://api.weather.gov"
	APIKey    = "github.com/icodealot/noaa" // User-Agent default value
	APIAccept = "application/ld+json"       // Changes may affect struct mappings below
)

// Cache used for point lookup to save some HTTP round trips
// key is expected to be PointsResponse.ID
var pointsCache = map[string]*PointsResponse{}

// PointsResponse holds the JSON values from /points/<lat,lon>
type PointsResponse struct {
	ID                          string `json:"@id"`
	CWA                         string `json:"cwa"`
	Office                      string `json:"forecastOffice"`
	GridX                       int64  `json:"gridX"`
	GridY                       int64  `json:"gridY"`
	GridID                      string `json:"gridId"`
	County                      string `json:"county"`
	FireWeatherZone             string `json:"fireWeatherZone"`
	EndpointForecast            string `json:"forecast"`
	EndpointForecastHourly      string `json:"forecastHourly"`
	EndpointObservationStations string `json:"observationStations"`
	EndpointForecastGridData    string `json:"forecastGridData"`
	Timezone                    string `json:"timeZone"`
	RadarStation                string `json:"radarStation"`
}

// OfficeAddress holds the JSON values for the address of an OfficeResponse
type OfficeAddress struct {
	Type          string `json:"@type"`
	StreetAddress string `json:"streetAddress"`
	Locality      string `json:"addressLocality"`
	Region        string `json:"addressRegion"`
	PostalCode    string `json:"postalCode"`
}

// OfficeResponse holds the JSON values from /offices/<id>
type OfficeResponse struct {
	Type                        string        `json:"@type"`
	URI                         string        `json:"@id"`
	ID                          string        `json:"id"`
	Name                        string        `json:"name"`
	Address                     OfficeAddress `json:"address"`
	Telephone                   string        `json:"telephone"`
	FaxNumber                   string        `json:"faxNumber"`
	Email                       string        `json:"email"`
	SameAs                      string        `json:"sameAs"`
	NWSRegion                   string        `json:"nwsRegion"`
	ParentOrganization          string        `json:"parentOrganization"`
	ResponsibleCounties         []string      `json:"responsibleCounties"`
	ResponsibleForecastZones    []string      `json:"responsibleForecastZones"`
	ResponsibleFireZones        []string      `json:"responsibleFireZones"`
	ApprovedObservationStations []string      `json:"approvedObservationStations"`
}

// StationsResponse holds the JSON values from /points/<lat,lon>/stations
type StationsResponse struct {
	Stations []string `json:"observationStations"`
}

// ForecastElevation holds the JSON values for a forecast response's elevation.
type ForecastElevation struct {
	Value float64 `json:"value"`
	Units string  `json:"unitCode"`
}

type ForecastHourlyElevation struct {
	Value          float64 `json:"value"`
	Max            float64 `json:"maxValue"`
	Min            float64 `json:"minValue"`
	UnitCode       string  `json:"unitCode"`
	QualityControl string  `json:"qualityControl"`
}

// ForecastResponsePeriod holds the JSON values for a period within a forecast response.
type ForecastResponsePeriod struct {
	ID               int32   `json:"number"`
	Name             string  `json:"name"`
	StartTime        string  `json:"startTime"`
	EndTime          string  `json:"endTime"`
	IsDaytime        bool    `json:"isDaytime"`
	Temperature      float64 `json:"temperature"`
	TemperatureUnit  string  `json:"temperatureUnit"`
	TemperatureTrend string  `json:"temperatureTrend"`
	WindSpeed        string  `json:"windSpeed"`
	WindDirection    string  `json:"windDirection"`
	Icon             string  `json:"icon"`
	Summary          string  `json:"shortForecast"`
	Details          string  `json:"detailedForecast"`
}

// ForecastResponsePeriodHourly provides the JSON value for a period within an hourly forecast.
type ForecastResponsePeriodHourly struct {
	ForecastResponsePeriod
}

// ForecastResponse holds the JSON values from /gridpoints/<cwa>/<x,y>/forecast"
type ForecastResponse struct {
	// capture data from the forecast
	Updated   string                   `json:"updated"`
	Units     string                   `json:"units"`
	Elevation ForecastElevation        `json:"elevation"`
	Periods   []ForecastResponsePeriod `json:"periods"`
	Point     *PointsResponse
}

// WeatherValueItem holds the JSON values for a weather.values[x].value.
type WeatherValueItem struct {
	Coverage  string `json:"coverage"`
	Weather   string `json:"weather"`
	Intensity string `json:"intensity"`
}

// WeatherValue holds the JSON value for a weather.values[x] value.
type WeatherValue struct {
	ValidTime string             `json:"validTime"` // ISO 8601 time interval, e.g. 2019-07-04T18:00:00+00:00/PT3H
	Value     []WeatherValueItem `json:"value"`
}

// Weather holds the JSON value for the weather object.
type Weather struct {
	Values []WeatherValue `json:"values"`
}

// HazardValueItem holds a value item from a GridpointForecastResponse's
// hazard.values[x].value[x].
type HazardValueItem struct {
	Phenomenon   string `json:"phenomenon"`
	Significance string `json:"significance"`
	EventNumber  int32  `json:"event_number"`
}

// HazardValue holds a hazard value from a GridpointForecastResponse's
// hazard.values[x].
type HazardValue struct {
	ValidTime string            `json:"validTime"` // ISO 8601 time interval, e.g. 2019-07-04T18:00:00+00:00/PT3H
	Value     []HazardValueItem `json:"value"`
}

// Hazard holds a slice of HazardValue items from a GridpointForecastResponse hazards
type Hazard struct {
	Values []HazardValue `json:"values"`
}

// HourlyForecastResponse holds the JSON values for the hourly forecast.
type HourlyForecastResponse struct {
	Updated           string                         `json:"updated"`
	Units             string                         `json:"units"`
	ForecastGenerator string                         `json:"forecastGenerator"`
	GeneratedAt       string                         `json:"generatedAt"`
	UpdateTime        string                         `json:"updateTime"`
	ValidTimes        string                         `json:"validTimes"`
	Periods           []ForecastResponsePeriodHourly `json:"periods"`
	Point             *PointsResponse
}

// GridpointForecastResponse holds the JSON values from /gridpoints/<cwa>/<x,y>"
// See https://weather-gov.github.io/api/gridpoints for information.
type GridpointForecastResponse struct {
	// capture data from the forecast
	Updated                          string                      `json:"updateTime"`
	Elevation                        ForecastElevation           `json:"elevation"`
	Weather                          Weather                     `json:"weather"`
	Hazards                          Hazard                      `json:"hazards"`
	Temperature                      GridpointForecastTimeSeries `json:"temperature"`
	Dewpoint                         GridpointForecastTimeSeries `json:"dewpoint"`
	MaxTemperature                   GridpointForecastTimeSeries `json:"maxTemperature"`
	MinTemperature                   GridpointForecastTimeSeries `json:"minTemperature"`
	RelativeHumidity                 GridpointForecastTimeSeries `json:"relativeHumidity"`
	ApparentTemperature              GridpointForecastTimeSeries `json:"apparentTemperature"`
	HeatIndex                        GridpointForecastTimeSeries `json:"heatIndex"`
	WindChill                        GridpointForecastTimeSeries `json:"windChill"`
	SkyCover                         GridpointForecastTimeSeries `json:"skyCover"`
	WindDirection                    GridpointForecastTimeSeries `json:"windDirection"`
	WindSpeed                        GridpointForecastTimeSeries `json:"windSpeed"`
	WindGust                         GridpointForecastTimeSeries `json:"windGust"`
	ProbabilityOfPrecipitation       GridpointForecastTimeSeries `json:"probabilityOfPrecipitation"`
	QuantitativePrecipitation        GridpointForecastTimeSeries `json:"quantitativePrecipitation"`
	IceAccumulation                  GridpointForecastTimeSeries `json:"iceAccumulation"`
	SnowfallAmount                   GridpointForecastTimeSeries `json:"snowfallAmount"`
	SnowLevel                        GridpointForecastTimeSeries `json:"snowLevel"`
	CeilingHeight                    GridpointForecastTimeSeries `json:"ceilingHeight"`
	Visibility                       GridpointForecastTimeSeries `json:"visibility"`
	TransportWindSpeed               GridpointForecastTimeSeries `json:"transportWindSpeed"`
	TransportWindDirection           GridpointForecastTimeSeries `json:"transportWindDirection"`
	MixingHeight                     GridpointForecastTimeSeries `json:"mixingHeight"`
	HainesIndex                      GridpointForecastTimeSeries `json:"hainesIndex"`
	LightningActivityLevel           GridpointForecastTimeSeries `json:"lightningActivityLevel"`
	TwentyFootWindSpeed              GridpointForecastTimeSeries `json:"twentyFootWindSpeed"`
	TwentyFootWindDirection          GridpointForecastTimeSeries `json:"twentyFootWindDirection"`
	WaveHeight                       GridpointForecastTimeSeries `json:"waveHeight"`
	WavePeriod                       GridpointForecastTimeSeries `json:"wavePeriod"`
	WaveDirection                    GridpointForecastTimeSeries `json:"waveDirection"`
	PrimarySwellHeight               GridpointForecastTimeSeries `json:"primarySwellHeight"`
	PrimarySwellDirection            GridpointForecastTimeSeries `json:"primarySwellDirection"`
	SecondarySwellHeight             GridpointForecastTimeSeries `json:"secondarySwellHeight"`
	SecondarySwellDirection          GridpointForecastTimeSeries `json:"secondarySwellDirection"`
	WavePeriod2                      GridpointForecastTimeSeries `json:"wavePeriod2"`
	WindWaveHeight                   GridpointForecastTimeSeries `json:"windWaveHeight"`
	DispersionIndex                  GridpointForecastTimeSeries `json:"dispersionIndex"`
	Pressure                         GridpointForecastTimeSeries `json:"pressure"`
	ProbabilityOfTropicalStormWinds  GridpointForecastTimeSeries `json:"probabilityOfTropicalStormWinds"`
	ProbabilityOfHurricaneWinds      GridpointForecastTimeSeries `json:"probabilityOfHurricaneWinds"`
	PotentialOf15mphWinds            GridpointForecastTimeSeries `json:"potentialOf15mphWinds"`
	PotentialOf25mphWinds            GridpointForecastTimeSeries `json:"potentialOf25mphWinds"`
	PotentialOf35mphWinds            GridpointForecastTimeSeries `json:"potentialOf35mphWinds"`
	PotentialOf45mphWinds            GridpointForecastTimeSeries `json:"potentialOf45mphWinds"`
	PotentialOf20mphWindGusts        GridpointForecastTimeSeries `json:"potentialOf20mphWindGusts"`
	PotentialOf30mphWindGusts        GridpointForecastTimeSeries `json:"potentialOf30mphWindGusts"`
	PotentialOf40mphWindGusts        GridpointForecastTimeSeries `json:"potentialOf40mphWindGusts"`
	PotentialOf50mphWindGusts        GridpointForecastTimeSeries `json:"potentialOf50mphWindGusts"`
	PotentialOf60mphWindGusts        GridpointForecastTimeSeries `json:"potentialOf60mphWindGusts"`
	GrasslandFireDangerIndex         GridpointForecastTimeSeries `json:"grasslandFireDangerIndex"`
	ProbabilityOfThunder             GridpointForecastTimeSeries `json:"probabilityOfThunder"`
	DavisStabilityIndex              GridpointForecastTimeSeries `json:"davisStabilityIndex"`
	AtmosphericDispersionIndex       GridpointForecastTimeSeries `json:"atmosphericDispersionIndex"`
	LowVisibilityOccurrenceRiskIndex GridpointForecastTimeSeries `json:"lowVisibilityOccurrenceRiskIndex"`
	Stability                        GridpointForecastTimeSeries `json:"stability"`
	RedFlagThreatIndex               GridpointForecastTimeSeries `json:"redFlagThreatIndex"`
	Point                            *PointsResponse
}

// GridpointForecastTimeSeriesValue holds the JSON value for a
// GridpointForecastTimeSeries' values[x] item.
type GridpointForecastTimeSeriesValue struct {
	ValidTime string  `json:"validTime"` // ISO 8601 time interval, e.g. 2019-07-04T18:00:00+00:00/PT3H
	Value     float64 `json:"value"`
}

// GridpointForecastTimeSeries holds a series of data from a gridpoint forecast
type GridpointForecastTimeSeries struct {
	Uom    string                             `json:"uom"` // Unit of Measure
	Values []GridpointForecastTimeSeriesValue `json:"values"`
}

// Call the weather.gov API. We could just use http.Get() but
// since we need to include some custom header values this helps.
func apiCall(endpoint string) (res *http.Response, err error) {
	endpoint = strings.Replace(endpoint, "http://", "https://", -1)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Accept", config.Accept)
	req.Header.Add("User-Agent", config.UserAgent)

	res, err = http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		return nil, errors.New(fmt.Sprintf("%d %s", res.StatusCode, res.Status))
	}

	return res, nil
}

// Points returns a set of useful endpoints for a given <lat,lon>
// or returns a cached object if appropriate
func Points(lat string, lon string) (points *PointsResponse, err error) {
	endpoint := fmt.Sprintf("%s/points/%s,%s", config.BaseURL, lat, lon)
	if pointsCache[endpoint] != nil {
		return pointsCache[endpoint], nil
	}
	res, err := apiCall(endpoint)

	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	if err = decoder.Decode(&points); err != nil {
		return nil, err
	}
	pointsCache[endpoint] = points
	return points, nil
}

// Office returns details for a specific office identified by its ID
// For example, https://api.weather.gov/offices/LOT (Chicago)
func Office(id string) (office *OfficeResponse, err error) {
	endpoint := fmt.Sprintf("%s/offices/%s", config.BaseURL, id)

	res, err := apiCall(endpoint)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	if err = decoder.Decode(&office); err != nil {
		return nil, err
	}
	return office, nil
}

// Stations returns an array of observation station IDs (urls)
func Stations(lat string, lon string) (stations *StationsResponse, err error) {
	point, err := Points(lat, lon)
	if err != nil {
		return nil, err
	}
	res, err := apiCall(point.EndpointObservationStations)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	if err = decoder.Decode(&stations); err != nil {
		return nil, err
	}
	return stations, nil
}

// Forecast returns an array of forecast observations (14 periods and 2/day max)
func Forecast(lat string, lon string) (forecast *ForecastResponse, err error) {
	query := ""
	point, err := Points(lat, lon)
	if err != nil {
		return nil, err
	}
	if config.Units != "" {
		query = "?units=" + config.Units
	}
	res, err := apiCall(point.EndpointForecast + query)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	if err = decoder.Decode(&forecast); err != nil {
		return nil, err
	}
	forecast.Point = point
	return forecast, nil
}

// GridpointForecast returns an array of raw forecast data
func GridpointForecast(lat string, long string) (forecast *GridpointForecastResponse, err error) {
	query := ""
	point, err := Points(lat, long)
	if err != nil {
		return nil, err
	}
	if config.Units != "" {
		query = "?units=" + config.Units
	}
	res, err := apiCall(point.EndpointForecastGridData + query)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	if err = decoder.Decode(&forecast); err != nil {
		return nil, err
	}
	forecast.Point = point
	return forecast, nil
}

// HourlyForecast returns an array of raw hourly forecast data
func HourlyForecast(lat string, long string) (forecast *HourlyForecastResponse, err error) {
	query := ""
	point, err := Points(lat, long)
	if err != nil {
		return nil, err
	}
	if config.Units != "" {
		query = "?units=" + config.Units
	}
	res, err := apiCall(point.EndpointForecastHourly + query)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	if err = decoder.Decode(&forecast); err != nil {
		return nil, err
	}
	forecast.Point = point
	return forecast, nil
}

type ObservationValue struct {
	Value          float64 `json:"value"`
	MaxValue       float64 `json:"maxValue"`
	MinValue       float64 `json:"minValue"`
	UnitCode       string  `json:"unitCode"`
	QualityControl string  `json:"qualityControl"`
}

type Observation struct {
	Elevation      ObservationValue `json:"elevation"`
	Station        string           `json:"station"`
	Timestamp      time.Time        `json:"timestamp"`
	PresentWeather []struct {
		Intensity  string `json:"intensity"`
		Modifier   string `json:"modifier"`
		Weather    string `json:"weather"`
		InVicinity bool   `json:"inVicinity"`
	} `json:"presentWeather"`
	Temperature               ObservationValue `json:"temperature"`
	Dewpoint                  ObservationValue `json:"dewpoint"`
	WindDirection             ObservationValue `json:"windDirection"`
	WindSpeed                 ObservationValue `json:"windSpeed"`
	WindGust                  ObservationValue `json:"windGust"`
	BarometricPressure        ObservationValue `json:"barometricPressure"`
	SeaLevelPressure          ObservationValue `json:"seaLevelPressure"`
	Visibility                ObservationValue `json:"visibility"`
	MaxTemperatureLast24Hours ObservationValue `json:"maxTemperatureLast24Hours"`
	MinTemperatureLast24Hours ObservationValue `json:"minTemperatureLast24Hours"`
	PrecipitationLastHour     ObservationValue `json:"precipitationLastHour"`
	PrecipitationLast3Hours   ObservationValue `json:"precipitationLast3Hours"`
	PrecipitationLast6Hours   ObservationValue `json:"precipitationLast6Hours"`
	RelativeHumidity          ObservationValue `json:"relativeHumidity"`
	WindChill                 ObservationValue `json:"windChill"`
	HeatIndex                 ObservationValue `json:"heatIndex"`
	CloudLayers               []struct {
		Base   ObservationValue `json:"base"`
		Amount string           `json:"amount"`
	} `json:"cloudLayers"`
}

func LatestStationObservation(stationID string) (observation Observation, err error) {
	// /stations/{stationId}/observations/latest
	endpoint := fmt.Sprintf("%s/observations/latest", stationID)

	res, err := apiCall(endpoint)
	if err != nil {
		return observation, fmt.Errorf("failed to get latest observations: %v", err)
	}
	defer res.Body.Close()
	decoder := json.NewDecoder(res.Body)
	observation = Observation{}
	if err = decoder.Decode(&observation); err != nil {
		return Observation{}, err
	}
	return observation, err
}

type Alert struct {
	ID          string `json:"@id"`
	Sent        string `json:"sent"`
	Effective   string `json:"effective"`
	Onset       string `json:"onset"`
	Expires     string `json:"expires"`
	Ends        string `json:"ends"`
	Status      string `json:"status"`
	Severity    string `json:"severity"`
	Certainty   string `json:"certainty"`
	Urgency     string `json:"urgency"`
	Event       string `json:"event"`
	Sender      string `json:"sender"`
	SenderName  string `json:"senderName"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	Instruction string `json:"instruction"`
	Response    string `json:"response"`
}

func Alerts(lat string, long string) ([]Alert, error) {
	u := fmt.Sprintf("%s%s%s,%s", config.BaseURL, "/alerts/active?point=", lat, long)
	res, err := apiCall(u)
	if err != nil {
		return []Alert{}, err
	}
	defer res.Body.Close()
	type Response struct {
		Data []Alert `json:"@graph"`
	}
	r := Response{}
	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		fmt.Println("error reading response: ", err)
		return []Alert{}, err
	}
	err = json.Unmarshal(data, &r)
	if err != nil {
		fmt.Println("error unmarshaling response: ", err)
		return []Alert{}, err
	}
	return r.Data, err
}
