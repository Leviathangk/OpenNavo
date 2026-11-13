package domain

import "strings"

// CityLocation is the map identity of a city: stable key, region and point.
type CityLocation struct {
	Key    string
	Region string
	Lat    float64
	Lng    float64
}

type cityEntry struct {
	names  []string
	region string
	lat    float64
	lng    float64
}

// cities keeps the submission form free-text while publishing a stable city key and
// point. Unknown cities are rejected instead of silently becoming country points.
var cities = map[string]map[string]cityEntry{
	"US": {
		"bay-area": {[]string{"san francisco", "sf", "bay area", "旧金山", "旧金山湾区"}, "americas", 37.7749, -122.4194},
		"seattle":  {[]string{"seattle", "西雅图"}, "americas", 47.6062, -122.3321},
		"nyc":      {[]string{"new york", "new york city", "nyc", "纽约"}, "americas", 40.7128, -74.0060},
		"boston":   {[]string{"boston", "波士顿"}, "americas", 42.3601, -71.0589},
		"austin":   {[]string{"austin", "奥斯汀"}, "americas", 30.2672, -97.7431},
		"portland": {[]string{"portland", "波特兰"}, "americas", 45.5152, -122.6784},
	},
	"CA": {
		"toronto": {[]string{"toronto", "多伦多"}, "americas", 43.6532, -79.3832},
	},
	"GB": {
		"london":     {[]string{"london", "伦敦"}, "europe", 51.5074, -0.1278},
		"nottingham": {[]string{"nottingham", "诺丁汉"}, "europe", 52.9548, -1.1581},
	},
	"DE": {
		"berlin": {[]string{"berlin", "柏林"}, "europe", 52.52, 13.405},
	},
	"FR": {
		"paris": {[]string{"paris", "巴黎"}, "europe", 48.8566, 2.3522},
	},
	"NL": {
		"amsterdam": {[]string{"amsterdam", "阿姆斯特丹"}, "europe", 52.3676, 4.9041},
		"the-hague": {[]string{"the hague", "den haag", "海牙"}, "europe", 52.0705, 4.3007},
	},
	"SE": {
		"stockholm": {[]string{"stockholm", "斯德哥尔摩"}, "europe", 59.3293, 18.0686},
	},
	"IT": {
		"milan": {[]string{"milan", "milano", "米兰"}, "europe", 45.4642, 9.19},
	},
	"AT": {
		"vienna": {[]string{"vienna", "wien", "维也纳"}, "europe", 48.2082, 16.3738},
	},
	"CZ": {
		"prague": {[]string{"prague", "praha", "布拉格"}, "europe", 50.0755, 14.4378},
	},
	"UA": {
		"kyiv": {[]string{"kyiv", "kiev", "基辅"}, "europe", 50.4501, 30.5234},
	},
	"CN": {
		"beijing":  {[]string{"beijing", "北京"}, "apac", 39.9042, 116.4074},
		"hangzhou": {[]string{"hangzhou", "杭州"}, "apac", 30.2741, 120.1551},
		"shenzhen": {[]string{"shenzhen", "深圳"}, "apac", 22.5431, 114.0579},
		"shanghai": {[]string{"shanghai", "上海"}, "apac", 31.2304, 121.4737},
	},
	"JP": {
		"tokyo": {[]string{"tokyo", "东京"}, "apac", 35.6762, 139.6503},
	},
	"KR": {
		"seoul": {[]string{"seoul", "首尔"}, "apac", 37.5665, 126.9780},
	},
	"IN": {
		"chennai":   {[]string{"chennai", "金奈"}, "apac", 13.0827, 80.2707},
		"bengaluru": {[]string{"bengaluru", "bangalore", "班加罗尔"}, "apac", 12.9716, 77.5946},
	},
	"AU": {
		"sydney":    {[]string{"sydney", "悉尼"}, "apac", -33.8688, 151.2093},
		"melbourne": {[]string{"melbourne", "墨尔本"}, "apac", -37.8136, 144.9631},
	},
	"IL": {
		"tel-aviv": {[]string{"tel aviv", "特拉维夫"}, "apac", 32.0853, 34.7818},
	},
}

// countryRegions keeps a submission that names only a country on the right map tab.
// The city table above stays authoritative for cities; this list covers the countries
// a developer is likely to name without a city, and IL sits in apac to match it.
var countryRegions = map[string]string{
	"US": "americas", "CA": "americas", "MX": "americas", "BR": "americas", "AR": "americas",
	"CL": "americas", "CO": "americas", "PE": "americas", "UY": "americas", "CR": "americas",

	"GB": "europe", "IE": "europe", "DE": "europe", "FR": "europe", "NL": "europe", "BE": "europe",
	"LU": "europe", "CH": "europe", "AT": "europe", "IT": "europe", "ES": "europe", "PT": "europe",
	"SE": "europe", "NO": "europe", "DK": "europe", "FI": "europe", "IS": "europe", "PL": "europe",
	"CZ": "europe", "SK": "europe", "HU": "europe", "RO": "europe", "BG": "europe", "GR": "europe",
	"UA": "europe", "RU": "europe", "EE": "europe", "LV": "europe", "LT": "europe", "HR": "europe",
	"SI": "europe", "RS": "europe", "TR": "europe",

	"CN": "apac", "JP": "apac", "KR": "apac", "IN": "apac", "SG": "apac", "MY": "apac",
	"ID": "apac", "TH": "apac", "VN": "apac", "PH": "apac", "TW": "apac", "HK": "apac",
	"AU": "apac", "NZ": "apac", "IL": "apac", "AE": "apac", "SA": "apac", "PK": "apac",
	"BD": "apac", "LK": "apac",
}

// RegionForCountry maps a country-only submission to its map region. An unknown
// country still gets a point, under the map's "other" tab.
func RegionForCountry(countryCode string) string {
	if region, ok := countryRegions[strings.ToUpper(strings.TrimSpace(countryCode))]; ok {
		return region
	}
	return "other"
}

// ResolveCity maps a free-text city to its key, region and point.
func ResolveCity(countryCode, name string) (CityLocation, bool) {
	country := strings.ToUpper(strings.TrimSpace(countryCode))
	normalized := normalizeCity(name)
	if normalized == "" {
		return CityLocation{}, false
	}
	for key, entry := range cities[country] {
		for _, candidate := range entry.names {
			if normalizeCity(candidate) == normalized {
				return CityLocation{Key: key, Region: entry.region, Lat: entry.lat, Lng: entry.lng}, true
			}
		}
	}
	return CityLocation{}, false
}

func normalizeCity(name string) string {
	value := strings.ToLower(strings.TrimSpace(name))
	value = strings.NewReplacer("_", "-", "，", ",", "、", ",").Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	return value
}
