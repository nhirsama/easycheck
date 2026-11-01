package qdgj

import (
	"errors"
	"math"
	"math/rand/v2"
)

const earthRadiusMeters = 6371008.8

// randomAllowedLocation 使用第一个字段完整的允许地点，在其半径的 90% 内采样。
func randomAllowedLocation(places []AllowedLocation) (Location, error) {
	for _, place := range places {
		if place.Longitude == nil || place.Latitude == nil || place.Radius == nil {
			continue
		}
		center := Location{Longitude: float64(*place.Longitude), Latitude: float64(*place.Latitude)}
		radius := float64(*place.Radius)
		if center.validateCoordinates() != nil || math.IsNaN(radius) || math.IsInf(radius, 0) || radius <= 0 || radius > math.Pi*earthRadiusMeters {
			continue
		}
		// 球面圆内按面积采样，最大偏移保留半径的 10% 余量。
		angle := 2 * math.Asin(math.Sqrt(rand.Float64())*math.Sin(radius*0.9/earthRadiusMeters/2))
		bearing := 2 * math.Pi * rand.Float64()
		latitude, longitude := center.Latitude*math.Pi/180, center.Longitude*math.Pi/180
		latitudeSin := math.Sin(latitude)*math.Cos(angle) + math.Cos(latitude)*math.Sin(angle)*math.Cos(bearing)
		newLatitude := math.Asin(math.Max(-1, math.Min(1, latitudeSin)))
		newLongitude := longitude + math.Atan2(math.Sin(bearing)*math.Sin(angle)*math.Cos(latitude), math.Cos(angle)-math.Sin(latitude)*math.Sin(newLatitude))
		newLongitude = math.Mod(newLongitude+3*math.Pi, 2*math.Pi) - math.Pi
		return Location{Longitude: newLongitude * 180 / math.Pi, Latitude: newLatitude * 180 / math.Pi}, nil
	}
	return Location{}, errors.New("任务缺少有效的允许地点中心坐标或半径，请显式提供经纬度")
}
