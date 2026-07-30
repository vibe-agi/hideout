package dns

import (
	"encoding/binary"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	dnsHeaderBytes       = 12
	maxDNSRecords        = 128
	maxDNSCompressionHop = 128
	maxDNSAnswers        = 64

	dnsTypeA     = 1
	dnsTypeCNAME = 5
	dnsTypeAAAA  = 28
	dnsClassIN   = 1
)

type wireMessage struct {
	transaction  uint16
	response     bool
	query        string
	queryType    string
	answers      []string
	ttl          time.Duration
	responseCode string
	limitations  []string
}

func parseWireMessage(
	wire []byte,
	maxTTL time.Duration,
) (wireMessage, error) {
	if len(wire) < dnsHeaderBytes {
		return wireMessage{}, ErrTruncatedMessage
	}
	flags := binary.BigEndian.Uint16(wire[2:4])
	if flags&0x7800 != 0 {
		return wireMessage{}, ErrUnsupportedMessage
	}
	if flags&0x0200 != 0 {
		return wireMessage{}, ErrTruncatedMessage
	}
	questionCount := int(binary.BigEndian.Uint16(wire[4:6]))
	answerCount := int(binary.BigEndian.Uint16(wire[6:8]))
	authorityCount := int(binary.BigEndian.Uint16(wire[8:10]))
	additionalCount := int(binary.BigEndian.Uint16(wire[10:12]))
	if questionCount != 1 ||
		answerCount+authorityCount+additionalCount > maxDNSRecords {
		return wireMessage{}, ErrUnsupportedMessage
	}
	query, offset, err := parseDNSName(wire, dnsHeaderBytes)
	if err != nil || query == "" {
		return wireMessage{}, ErrMalformedMessage
	}
	if offset+4 > len(wire) {
		return wireMessage{}, ErrTruncatedMessage
	}
	queryKind := binary.BigEndian.Uint16(wire[offset : offset+2])
	queryClass := binary.BigEndian.Uint16(wire[offset+2 : offset+4])
	offset += 4
	if queryClass != dnsClassIN {
		return wireMessage{}, ErrUnsupportedMessage
	}
	queryType := ""
	switch queryKind {
	case dnsTypeA:
		queryType = "A"
	case dnsTypeAAAA:
		queryType = "AAAA"
	default:
		return wireMessage{}, ErrUnsupportedMessage
	}

	result := wireMessage{
		transaction: binary.BigEndian.Uint16(wire[0:2]),
		response:    flags&0x8000 != 0,
		query:       query,
		queryType:   queryType,
		responseCode: responseCode(
			byte(flags & 0x000f),
		),
	}
	if !result.response {
		if flags&0x000f != 0 || answerCount != 0 || authorityCount != 0 {
			return wireMessage{}, ErrMalformedMessage
		}
	}
	answerRecords := make([]resourceRecord, 0, answerCount)
	for index := 0; index < answerCount+authorityCount+additionalCount; index++ {
		record, next, err := parseResourceRecord(wire, offset)
		if err != nil {
			return wireMessage{}, err
		}
		offset = next
		if index < answerCount {
			answerRecords = append(answerRecords, record)
		}
	}
	if offset != len(wire) {
		return wireMessage{}, ErrMalformedMessage
	}

	reachableNames := map[string]struct{}{query: {}}
	usedCNAME := make(map[int]struct{})
	minTTL := uint32(0)
	ttlObserved := false
	for pass := 0; pass < len(answerRecords); pass++ {
		changed := false
		for index, record := range answerRecords {
			if record.class != dnsClassIN ||
				record.kind != dnsTypeCNAME {
				continue
			}
			if _, reachable := reachableNames[record.name]; !reachable {
				continue
			}
			target, next, err := parseDNSName(wire, record.dataOffset)
			if err != nil ||
				target == "" ||
				next != record.dataOffset+len(record.data) {
				return wireMessage{}, ErrMalformedMessage
			}
			if _, used := usedCNAME[index]; !used {
				usedCNAME[index] = struct{}{}
				minTTL, ttlObserved = minimumTTL(
					minTTL,
					ttlObserved,
					record.ttl,
				)
			}
			if _, exists := reachableNames[target]; !exists {
				reachableNames[target] = struct{}{}
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	answerSet := make(map[string]struct{}, min(answerCount, maxDNSAnswers))
	for _, record := range answerRecords {
		if record.class != dnsClassIN ||
			record.kind != queryKind {
			continue
		}
		if _, reachable := reachableNames[record.name]; !reachable {
			continue
		}
		answer := ""
		switch record.kind {
		case dnsTypeA:
			if len(record.data) != net.IPv4len {
				return wireMessage{}, ErrMalformedMessage
			}
			answer = net.IP(record.data).String()
		case dnsTypeAAAA:
			if len(record.data) != net.IPv6len {
				return wireMessage{}, ErrMalformedMessage
			}
			answer = net.IP(record.data).String()
		}
		if answer == "" {
			continue
		}
		minTTL, ttlObserved = minimumTTL(
			minTTL,
			ttlObserved,
			record.ttl,
		)
		if _, exists := answerSet[answer]; exists {
			continue
		}
		if len(answerSet) >= maxDNSAnswers {
			return wireMessage{}, ErrMessageLimit
		}
		answerSet[answer] = struct{}{}
	}
	for answer := range answerSet {
		result.answers = append(result.answers, answer)
	}
	sort.Strings(result.answers)
	if result.responseCode != "NOERROR" {
		result.answers = nil
		minTTL = 0
		ttlObserved = false
	}
	ttlCapped := false
	if ttlObserved {
		observedTTL := time.Duration(minTTL) * time.Second
		if observedTTL > maxTTL {
			result.ttl = maxTTL
			ttlCapped = true
		} else {
			result.ttl = observedTTL
		}
	}
	if ttlCapped {
		result.limitations = []string{"dns-ttl-capped"}
	}
	return result, nil
}

type resourceRecord struct {
	name       string
	kind       uint16
	class      uint16
	ttl        uint32
	dataOffset int
	data       []byte
}

func parseResourceRecord(
	wire []byte,
	offset int,
) (resourceRecord, int, error) {
	name, offset, err := parseDNSName(wire, offset)
	if err != nil {
		return resourceRecord{}, 0, err
	}
	if offset+10 > len(wire) {
		return resourceRecord{}, 0, ErrTruncatedMessage
	}
	kind := binary.BigEndian.Uint16(wire[offset : offset+2])
	class := binary.BigEndian.Uint16(wire[offset+2 : offset+4])
	ttl := binary.BigEndian.Uint32(wire[offset+4 : offset+8])
	length := int(binary.BigEndian.Uint16(wire[offset+8 : offset+10]))
	offset += 10
	if length > len(wire)-offset {
		return resourceRecord{}, 0, ErrTruncatedMessage
	}
	return resourceRecord{
		name:       name,
		kind:       kind,
		class:      class,
		ttl:        ttl,
		dataOffset: offset,
		data:       wire[offset : offset+length],
	}, offset + length, nil
}

func parseDNSName(
	wire []byte,
	start int,
) (string, int, error) {
	if start < 0 || start >= len(wire) {
		return "", 0, ErrTruncatedMessage
	}
	labels := make([]string, 0, 8)
	visited := make(map[int]struct{}, 8)
	offset := start
	next := -1
	wireLength := 1
	for hop := 0; hop < maxDNSCompressionHop; hop++ {
		if offset >= len(wire) {
			return "", 0, ErrTruncatedMessage
		}
		if _, exists := visited[offset]; exists {
			return "", 0, ErrMalformedMessage
		}
		visited[offset] = struct{}{}
		length := wire[offset]
		switch length & 0xc0 {
		case 0x00:
			offset++
			if length == 0 {
				if next < 0 {
					next = offset
				}
				name := strings.Join(labels, ".")
				if len(name) > 253 {
					return "", 0, ErrMessageLimit
				}
				return name, next, nil
			}
			if length > 63 ||
				int(length) > len(wire)-offset {
				return "", 0, ErrMalformedMessage
			}
			labelBytes := wire[offset : offset+int(length)]
			label, ok := canonicalLabel(labelBytes)
			if !ok {
				return "", 0, ErrMalformedMessage
			}
			labels = append(labels, label)
			wireLength += int(length) + 1
			if wireLength > 255 {
				return "", 0, ErrMessageLimit
			}
			offset += int(length)
		case 0xc0:
			if offset+2 > len(wire) {
				return "", 0, ErrTruncatedMessage
			}
			pointer := int(length&0x3f)<<8 | int(wire[offset+1])
			if pointer >= offset ||
				pointer < dnsHeaderBytes ||
				pointer >= len(wire) {
				return "", 0, ErrMalformedMessage
			}
			if next < 0 {
				next = offset + 2
			}
			offset = pointer
		default:
			return "", 0, ErrUnsupportedMessage
		}
	}
	return "", 0, ErrMessageLimit
}

func canonicalLabel(value []byte) (string, bool) {
	if len(value) == 0 ||
		len(value) > 63 ||
		value[0] == '-' ||
		value[len(value)-1] == '-' {
		return "", false
	}
	result := make([]byte, len(value))
	for index, current := range value {
		switch {
		case current >= 'A' && current <= 'Z':
			result[index] = current + ('a' - 'A')
		case current >= 'a' && current <= 'z':
			result[index] = current
		case current >= '0' && current <= '9':
			result[index] = current
		case current == '-':
			result[index] = current
		default:
			return "", false
		}
	}
	return string(result), true
}

func responseCode(value byte) string {
	switch value {
	case 0:
		return "NOERROR"
	case 1:
		return "FORMERR"
	case 2:
		return "SERVFAIL"
	case 3:
		return "NXDOMAIN"
	case 4:
		return "NOTIMP"
	case 5:
		return "REFUSED"
	default:
		return "RCODE" + strconv.Itoa(int(value))
	}
}

func minimumTTL(
	current uint32,
	observed bool,
	candidate uint32,
) (uint32, bool) {
	if !observed || candidate < current {
		return candidate, true
	}
	return current, true
}
