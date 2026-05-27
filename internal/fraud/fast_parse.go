package fraud

import "bytes"

// rawParsedTx holds transaction fields from a single forward scan.
type rawParsedTx struct {
	amount       float64
	installments int
	requestedAt  []byte // sub-slice of input buffer, no copy
}

type rawParsedCust struct {
	avgAmount      float64
	txCount24h     int
	knownMerchants []byte // raw JSON array bytes (views into input buffer)
}

type rawParsedMerch struct {
	id        []byte
	mcc       []byte
	avgAmount float64
}

type rawParsedTerm struct {
	isOnline    bool
	cardPresent bool
	kmFromHome  float64
}

type rawParsedLastTx struct {
	timestamp     []byte
	kmFromCurrent float64
}

type parsedTimestamp struct {
	hour    int
	weekday int
	unixSec int64
}

var errBadJSON = &requestParseError{"bad json"}

type requestParseError struct{ msg string }

func (e *requestParseError) Error() string { return e.msg }

// ParseAndScore parses a raw JSON fraud-score request body in a single forward
// pass (no reflection, no allocations for numeric/bool fields) and returns the
// raw fraud neighbor count (0–5). Returns an error only if the JSON cannot be
// parsed at all; malformed fields are silently treated as zero values.
func (e *Engine) ParseAndScore(data []byte) (int, error) {
	if len(data) < 2 {
		return 0, errBadJSON
	}
	txObj, custObj, merchObj, termObj, lastTxObj := scanTopLevel(data)
	if txObj == nil {
		return 0, errBadJSON
	}

	tx := parseTxFieldsRaw(txObj)
	cust := parseCustFieldsRaw(custObj)
	merch := parseMerchFieldsRaw(merchObj)
	term := parseTermFieldsRaw(termObj)
	ltx := parseLastTxFieldsRaw(lastTxObj)

	vec := e.buildVectorFast(tx, cust, merch, term, ltx)
	return e.knnSearch(vec), nil
}

// buildVectorFast is the zero-allocation equivalent of buildVector.
// It uses pre-parsed raw byte slices rather than a decoded Request struct.
func (e *Engine) buildVectorFast(
	tx rawParsedTx, cust rawParsedCust, merch rawParsedMerch,
	term rawParsedTerm, ltx rawParsedLastTx,
) [dims]uint16 {
	n := &e.norm

	reqTS := parseTimestampBytes(tx.requestedAt)
	hourUTC := float64(reqTS.hour) / 23.0
	dow := float64(reqTS.weekday) / 6.0

	dim5, dim6 := -1.0, -1.0
	if ltx.timestamp != nil {
		lastSec := parseTimestampBytes(ltx.timestamp).unixSec
		minutes := float64(reqTS.unixSec-lastSec) / 60.0
		dim5 = clamp(minutes / n.MaxMinutes)
		dim6 = clamp(ltx.kmFromCurrent / n.MaxKm)
	}

	unknownMerchant := 1.0
	if cust.knownMerchants != nil && merch.id != nil {
		if merchantIDInArray(cust.knownMerchants, merch.id) {
			unknownMerchant = 0
		}
	}

	// MCC lookup requires a string key; use unsafe-free conversion via string().
	// MCC codes are typically 4 bytes so this is a tiny allocation.
	mccRisk, ok := e.mccRisk[string(merch.mcc)]
	if !ok {
		mccRisk = 0.5
	}

	raw := [dims]float64{
		clamp(tx.amount / n.MaxAmount),
		clamp(float64(tx.installments) / n.MaxInstallments),
		clamp((tx.amount / cust.avgAmount) / n.AmountVsAvgRatio),
		hourUTC,
		dow,
		dim5,
		dim6,
		clamp(term.kmFromHome / n.MaxKm),
		clamp(float64(cust.txCount24h) / n.MaxTxCount24h),
		boolF64(term.isOnline),
		boolF64(term.cardPresent),
		unknownMerchant,
		mccRisk,
		clamp(merch.avgAmount / n.MaxMerchantAvgAmt),
	}

	var vec [dims]uint16
	for i, v := range raw {
		vec[i] = encodeVal(v)
	}
	return vec
}

// ─── Top-level scanner ────────────────────────────────────────────────────────

// scanTopLevel finds all 5 named top-level JSON objects in one forward pass.
// Returns nil slices for any object not present (e.g. last_transaction = null).
func scanTopLevel(data []byte) (tx, cust, merch, term, lastTx []byte) {
	i := 0
	n := len(data)

	// Skip to opening '{'
	for i < n && data[i] != '{' {
		i++
	}
	if i >= n {
		return
	}
	i++

	depth := 1
	for i < n && depth > 0 {
		switch data[i] {
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
		case '"':
			// Read key
			j := i + 1
			for j < n {
				if data[j] == '\\' {
					j += 2
					continue
				}
				if data[j] == '"' {
					break
				}
				j++
			}
			key := data[i+1 : j]
			i = j + 1

			if depth == 1 {
				// Advance to ':'
				for i < n && data[i] != ':' {
					i++
				}
				i++
				// Skip whitespace
				for i < n && isWS(data[i]) {
					i++
				}
				if i >= n {
					return
				}

				if data[i] == '{' {
					objStart := i
					objDepth := 1
					i++
					for i < n && objDepth > 0 {
						switch data[i] {
						case '{':
							objDepth++
						case '}':
							objDepth--
						case '"':
							i++
							for i < n {
								if data[i] == '\\' {
									i += 2
									continue
								}
								if data[i] == '"' {
									break
								}
								i++
							}
						}
						i++
					}
					// key dispatch: length + first char is sufficient
					switch len(key) {
					case 11: // "transaction"
						if key[0] == 't' && key[1] == 'r' {
							tx = data[objStart:i]
						}
					case 8:
						switch key[0] {
						case 'c': // "customer"
							cust = data[objStart:i]
						case 'm': // "merchant"
							merch = data[objStart:i]
						case 't': // "terminal"
							term = data[objStart:i]
						}
					case 16: // "last_transaction"
						if key[0] == 'l' {
							lastTx = data[objStart:i]
						}
					}
				} else {
					skipValueAt(data, &i, n)
				}
			}
		default:
			i++
		}
	}
	return
}

// ─── Object field parsers ─────────────────────────────────────────────────────

func parseTxFieldsRaw(tx []byte) rawParsedTx {
	var f rawParsedTx
	i, n := skipToObjectOpen(tx)
	if i < 0 {
		return f
	}
	for i < n {
		skipCommaWS(tx, &i, n)
		if i >= n || tx[i] == '}' {
			break
		}
		if tx[i] != '"' {
			i++
			continue
		}
		key, next := readKey(tx, i+1, n)
		i = next
		skipColon(tx, &i, n)
		skipWS(tx, &i, n)
		if i >= n {
			break
		}
		switch {
		case len(key) == 6 && key[0] == 'a': // "amount"
			f.amount, i = parseFloatInline(tx, i, n)
		case len(key) == 12 && key[0] == 'i': // "installments"
			f.installments, i = parseIntInline(tx, i, n)
		case len(key) == 12 && key[0] == 'r': // "requested_at"
			f.requestedAt, i = parseStringInline(tx, i, n)
		default:
			i = skipValueInline(tx, i, n)
		}
	}
	return f
}

func parseCustFieldsRaw(cust []byte) rawParsedCust {
	var f rawParsedCust
	i, n := skipToObjectOpen(cust)
	if i < 0 {
		return f
	}
	for i < n {
		skipCommaWS(cust, &i, n)
		if i >= n || cust[i] == '}' {
			break
		}
		if cust[i] != '"' {
			i++
			continue
		}
		key, next := readKey(cust, i+1, n)
		i = next
		skipColon(cust, &i, n)
		skipWS(cust, &i, n)
		if i >= n {
			break
		}
		switch {
		case len(key) == 10 && key[0] == 'a': // "avg_amount"
			f.avgAmount, i = parseFloatInline(cust, i, n)
		case len(key) == 12 && key[0] == 't': // "tx_count_24h"
			f.txCount24h, i = parseIntInline(cust, i, n)
		case len(key) == 15 && key[0] == 'k': // "known_merchants"
			f.knownMerchants, i = parseArrayInline(cust, i, n)
		default:
			i = skipValueInline(cust, i, n)
		}
	}
	return f
}

func parseMerchFieldsRaw(merch []byte) rawParsedMerch {
	var f rawParsedMerch
	i, n := skipToObjectOpen(merch)
	if i < 0 {
		return f
	}
	for i < n {
		skipCommaWS(merch, &i, n)
		if i >= n || merch[i] == '}' {
			break
		}
		if merch[i] != '"' {
			i++
			continue
		}
		key, next := readKey(merch, i+1, n)
		i = next
		skipColon(merch, &i, n)
		skipWS(merch, &i, n)
		if i >= n {
			break
		}
		switch {
		case len(key) == 2 && key[0] == 'i': // "id"
			f.id, i = parseStringInline(merch, i, n)
		case len(key) == 3 && key[0] == 'm': // "mcc"
			f.mcc, i = parseStringInline(merch, i, n)
		case len(key) == 10 && key[0] == 'a': // "avg_amount"
			f.avgAmount, i = parseFloatInline(merch, i, n)
		default:
			i = skipValueInline(merch, i, n)
		}
	}
	return f
}

func parseTermFieldsRaw(term []byte) rawParsedTerm {
	var f rawParsedTerm
	i, n := skipToObjectOpen(term)
	if i < 0 {
		return f
	}
	for i < n {
		skipCommaWS(term, &i, n)
		if i >= n || term[i] == '}' {
			break
		}
		if term[i] != '"' {
			i++
			continue
		}
		key, next := readKey(term, i+1, n)
		i = next
		skipColon(term, &i, n)
		skipWS(term, &i, n)
		if i >= n {
			break
		}
		switch {
		case len(key) == 9 && key[0] == 'i': // "is_online"
			f.isOnline, i = parseBoolInline(term, i, n)
		case len(key) == 12 && key[0] == 'c': // "card_present"
			f.cardPresent, i = parseBoolInline(term, i, n)
		case len(key) == 12 && key[0] == 'k': // "km_from_home"
			f.kmFromHome, i = parseFloatInline(term, i, n)
		default:
			i = skipValueInline(term, i, n)
		}
	}
	return f
}

func parseLastTxFieldsRaw(ltx []byte) rawParsedLastTx {
	var f rawParsedLastTx
	i, n := skipToObjectOpen(ltx)
	if i < 0 {
		return f
	}
	for i < n {
		skipCommaWS(ltx, &i, n)
		if i >= n || ltx[i] == '}' {
			break
		}
		if ltx[i] != '"' {
			i++
			continue
		}
		key, next := readKey(ltx, i+1, n)
		i = next
		skipColon(ltx, &i, n)
		skipWS(ltx, &i, n)
		if i >= n {
			break
		}
		switch {
		case len(key) == 9 && key[0] == 't': // "timestamp"
			f.timestamp, i = parseStringInline(ltx, i, n)
		case len(key) == 15 && key[0] == 'k': // "km_from_current"
			f.kmFromCurrent, i = parseFloatInline(ltx, i, n)
		default:
			i = skipValueInline(ltx, i, n)
		}
	}
	return f
}

// ─── Inline value parsers (return value + next index) ─────────────────────────

func parseFloatInline(data []byte, i, n int) (float64, int) {
	// skip leading whitespace (caller already stripped, but be safe)
	for i < n && isWS(data[i]) {
		i++
	}
	start := i
	for i < n {
		b := data[i]
		if (b >= '0' && b <= '9') || b == '.' || b == '-' || b == '+' || b == 'e' || b == 'E' {
			i++
		} else {
			break
		}
	}
	if start == i {
		return 0, i
	}
	return parseFloatFast(data[start:i]), i
}

func parseIntInline(data []byte, i, n int) (int, int) {
	for i < n && isWS(data[i]) {
		i++
	}
	neg := false
	if i < n && data[i] == '-' {
		neg = true
		i++
	}
	val := 0
	for i < n && data[i] >= '0' && data[i] <= '9' {
		val = val*10 + int(data[i]-'0')
		i++
	}
	if neg {
		val = -val
	}
	return val, i
}

func parseStringInline(data []byte, i, n int) ([]byte, int) {
	for i < n && isWS(data[i]) {
		i++
	}
	if i >= n || data[i] != '"' {
		return nil, i
	}
	i++
	start := i
	for i < n {
		if data[i] == '\\' {
			i += 2
			continue
		}
		if data[i] == '"' {
			return data[start:i], i + 1
		}
		i++
	}
	return nil, i
}

func parseBoolInline(data []byte, i, n int) (bool, int) {
	for i < n && isWS(data[i]) {
		i++
	}
	if i+3 < n && data[i] == 't' && data[i+1] == 'r' && data[i+2] == 'u' && data[i+3] == 'e' {
		return true, i + 4
	}
	return false, i
}

func parseArrayInline(data []byte, i, n int) ([]byte, int) {
	for i < n && isWS(data[i]) {
		i++
	}
	if i >= n || data[i] != '[' {
		return nil, i
	}
	start := i
	depth := 1
	i++
	for i < n && depth > 0 {
		switch data[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '"':
			i++
			for i < n {
				if data[i] == '\\' {
					i += 2
					continue
				}
				if data[i] == '"' {
					break
				}
				i++
			}
		}
		i++
	}
	// data[start:i-1] = from '[' to just before ']'
	return data[start : i-1], i
}

func skipValueInline(data []byte, i, n int) int {
	if i >= n {
		return i
	}
	switch data[i] {
	case '"':
		i++
		for i < n {
			if data[i] == '\\' {
				i += 2
				continue
			}
			if data[i] == '"' {
				return i + 1
			}
			i++
		}
	case 't':
		return i + 4 // "true"
	case 'f':
		return i + 5 // "false"
	case 'n':
		return i + 4 // "null"
	case '[':
		depth := 1
		i++
		for i < n && depth > 0 {
			switch data[i] {
			case '[':
				depth++
			case ']':
				depth--
			case '"':
				i++
				for i < n {
					if data[i] == '\\' {
						i += 2
						continue
					}
					if data[i] == '"' {
						break
					}
					i++
				}
			}
			i++
		}
	case '{':
		depth := 1
		i++
		for i < n && depth > 0 {
			switch data[i] {
			case '{':
				depth++
			case '}':
				depth--
			case '"':
				i++
				for i < n {
					if data[i] == '\\' {
						i += 2
						continue
					}
					if data[i] == '"' {
						break
					}
					i++
				}
			}
			i++
		}
	default: // number
		for i < n {
			b := data[i]
			if (b >= '0' && b <= '9') || b == '.' || b == '-' || b == '+' || b == 'e' || b == 'E' {
				i++
			} else {
				break
			}
		}
	}
	return i
}

// skipValueAt is a pointer-receiver variant used in scanTopLevel.
func skipValueAt(data []byte, i *int, n int) {
	*i = skipValueInline(data, *i, n)
}

// ─── Float parser ─────────────────────────────────────────────────────────────

// parseFloatFast parses a decimal float from bytes without using strconv.
// Handles sign, integer part, and fractional part. No exponent support
// (competition values never use scientific notation).
func parseFloatFast(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	neg := false
	j := 0
	if b[j] == '-' {
		neg = true
		j++
	} else if b[j] == '+' {
		j++
	}

	var intPart int64
	for j < len(b) && b[j] >= '0' && b[j] <= '9' {
		intPart = intPart*10 + int64(b[j]-'0')
		j++
	}

	var fracPart int64
	var fracDiv int64 = 1
	if j < len(b) && b[j] == '.' {
		j++
		for j < len(b) && b[j] >= '0' && b[j] <= '9' {
			fracPart = fracPart*10 + int64(b[j]-'0')
			fracDiv *= 10
			j++
		}
	}

	var val float64
	if fracDiv > 1 {
		val = float64(intPart) + float64(fracPart)/float64(fracDiv)
	} else {
		val = float64(intPart)
	}
	if neg {
		val = -val
	}
	return val
}

// ─── Timestamp parser ─────────────────────────────────────────────────────────

// parseTimestampBytes extracts hour, day-of-week (Mon=0), and Unix seconds
// from an ISO-8601 timestamp like "2024-01-15T14:32:00Z" or "…+00:00".
// Returns zero values for any malformed input.
func parseTimestampBytes(b []byte) parsedTimestamp {
	if len(b) < 19 || b[4] != '-' || b[7] != '-' || b[10] != 'T' || b[13] != ':' || b[16] != ':' {
		return parsedTimestamp{}
	}

	year := int(b[0]-'0')*1000 + int(b[1]-'0')*100 + int(b[2]-'0')*10 + int(b[3]-'0')
	month := int(b[5]-'0')*10 + int(b[6]-'0')
	day := int(b[8]-'0')*10 + int(b[9]-'0')
	hour := int(b[11]-'0')*10 + int(b[12]-'0')
	min := int64(b[14]-'0')*10 + int64(b[15]-'0')
	sec := int64(b[17]-'0')*10 + int64(b[18]-'0')

	// Day of week via Tomohiko Sakamoto's algorithm (0=Sun … 6=Sat → remap to Mon=0).
	t := [12]int{0, 3, 2, 5, 0, 3, 5, 1, 4, 6, 2, 4}
	yAdj := year
	if month < 3 {
		yAdj--
	}
	wd := (yAdj + yAdj/4 - yAdj/100 + yAdj/400 + t[month-1] + day) % 7
	weekday := (wd + 6) % 7 // Mon=0, …, Sun=6

	// Unix timestamp via proleptic Gregorian calendar.
	iYear := int64(year)
	iMonth := int64(month)
	if iMonth <= 2 {
		iYear--
		iMonth += 12
	}
	days := iYear*365 + iYear/4 - iYear/100 + iYear/400 +
		(153*(iMonth-3)+2)/5 + int64(day) - 719469
	unixSec := days*86400 + int64(hour)*3600 + min*60 + sec

	return parsedTimestamp{hour: hour, weekday: weekday, unixSec: unixSec}
}

// ─── Merchant array check ─────────────────────────────────────────────────────

// merchantIDInArray checks whether target appears as a string value in the
// raw JSON array bytes returned by parseArrayInline (includes opening '[').
func merchantIDInArray(arrBytes, target []byte) bool {
	i := 0
	n := len(arrBytes)
	for i < n {
		if arrBytes[i] != '"' {
			i++
			continue
		}
		i++
		start := i
		for i < n {
			if arrBytes[i] == '\\' {
				i += 2
				continue
			}
			if arrBytes[i] == '"' {
				break
			}
			i++
		}
		if bytes.Equal(arrBytes[start:i], target) {
			return true
		}
		i++
	}
	return false
}

// ─── Scanner helpers ──────────────────────────────────────────────────────────

func isWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// skipToObjectOpen advances past whitespace to the first '{' and returns
// (index after '{', len(data)). Returns (-1, 0) if no '{' found.
func skipToObjectOpen(data []byte) (int, int) {
	n := len(data)
	for i := 0; i < n; i++ {
		if data[i] == '{' {
			return i + 1, n
		}
	}
	return -1, 0
}

func skipCommaWS(data []byte, i *int, n int) {
	for *i < n && (isWS(data[*i]) || data[*i] == ',') {
		*i++
	}
}

func skipWS(data []byte, i *int, n int) {
	for *i < n && isWS(data[*i]) {
		*i++
	}
}

func skipColon(data []byte, i *int, n int) {
	for *i < n && data[*i] != ':' {
		*i++
	}
	if *i < n {
		*i++
	}
}

// readKey reads a JSON key starting at position start (after opening '"').
// Returns the key bytes and the index AFTER the closing '"' + ':'.
func readKey(data []byte, start, n int) ([]byte, int) {
	i := start
	for i < n {
		if data[i] == '\\' {
			i += 2
			continue
		}
		if data[i] == '"' {
			return data[start:i], i + 1
		}
		i++
	}
	return data[start:i], i
}
