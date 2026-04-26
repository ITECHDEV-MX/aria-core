// Package redactor implements PII scrubbing, sensitivity tagging, and LLM
// egress audit for ARIA Core. It is the privacy boundary between developer
// queries and external LLM providers (Anthropic / OpenAI / etc).
//
// patterns.go: Spanish-locale regex library for entity detection.
// Detects RFC, CURP, email, MX phone, monto, CLABE/IBAN, address, credit card.
package redactor

import (
	"regexp"
	"strconv"
	"strings"
)

// PatternType is a typed enum of the entities the redactor knows how to detect.
type PatternType string

const (
	PatternRFC        PatternType = "rfc"
	PatternCURP       PatternType = "curp"
	PatternEmail      PatternType = "email"
	PatternPhone      PatternType = "phone"
	PatternAmount     PatternType = "amount"
	PatternLegalName  PatternType = "legal_name"
	PatternCLABE      PatternType = "clabe"
	PatternIBAN       PatternType = "iban"
	PatternAddress    PatternType = "address"
	PatternCreditCard PatternType = "credit_card"
	// Reference-by-ID lookups (resolved via aliases store):
	PatternClient PatternType = "client"
	PatternQuote  PatternType = "quote"
	PatternLead   PatternType = "lead"
)

// patternDef pairs a regex with its category. Order matters when patterns can
// overlap; longer / more specific patterns are evaluated first.
type patternDef struct {
	Type    PatternType
	Re      *regexp.Regexp
	Filter  func(match string) bool // optional secondary validation (e.g. Luhn for cards)
	IsBoxed bool                    // already wrapped in word boundaries; skip boundary wrap
}

var (
	// RFC mexicano: 12 (persona moral) o 13 (persona física) caracteres.
	// Formato: [A-ZÑ&]{3,4}YYMMDD[A-Z\d]{3}. Word-boundary defensiva.
	reRFC = regexp.MustCompile(`(?i)\b[A-ZÑ&]{3,4}\d{6}[A-Z\d]{3}\b`)

	// CURP mexicana: 18 chars exactos.
	// [A-Z]{4}\d{6}[HM][A-Z]{5}[A-Z\d]\d
	reCURP = regexp.MustCompile(`(?i)\b[A-Z]{4}\d{6}[HM][A-Z]{5}[A-Z\d]\d\b`)

	// Email RFC-2822-ish.
	reEmail = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)

	// Teléfono mexicano: +52, lada, opcional paréntesis y guiones.
	// Acepta: "+52 81 1234 5678", "(81) 1234-5678", "8112345678", "81-1234-5678".
	rePhone = regexp.MustCompile(`(?:\+?52[\s\-]?)?(?:\(\d{2,3}\)|\d{2,3})[\s\-]?\d{3,4}[\s\-]?\d{4}`)

	// Monto: $ + dígitos opcionalmente con coma de miles + decimales opcional + sufijo opcional.
	reAmount = regexp.MustCompile(`(?i)\$\s?\d{1,3}(?:,\d{3})*(?:\.\d{2})?(?:\s?(?:MXN|USD|MN|pesos|d[oó]lares))?`)

	// Razón social legal: detecta sufijos S.A. de C.V., S. de R.L., A.C.
	reLegalName = regexp.MustCompile(`(?i)[A-Z][A-Za-zÁÉÍÓÚáéíóúÑñ&\s\.\-]{2,80}?[\s,]+(?:S\.?A\.?\s+de\s+C\.?V\.?|S\.?\s+de\s+R\.?L\.?(?:\s+de\s+C\.?V\.?)?|S\.?A\.?P\.?I\.?(?:\s+de\s+C\.?V\.?)?|A\.?C\.?)\b`)

	// CLABE (Mexican bank): 18 dígitos.
	reCLABE = regexp.MustCompile(`\b\d{18}\b`)

	// IBAN: 2 letras + 2 dígitos + hasta 30 alfanuméricos.
	reIBAN = regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{10,30}\b`)

	// Dirección con CP: "Calle / Av / Blvd / Avenida ... CP 12345" o ".. 12345, México".
	reAddress = regexp.MustCompile(`(?i)\b(?:calle|avenida|av\.?|blvd\.?|boulevard|carretera|carr\.?)\s+[A-Za-zÁÉÍÓÚáéíóúÑñ0-9\.,\s\-#]{3,80}?(?:CP\s*)?\d{5}\b`)

	// Tarjeta de crédito: 13-19 dígitos con grupos opcionales por espacios o guiones.
	reCreditCard = regexp.MustCompile(`\b(?:\d[\s\-]?){13,19}\b`)
)

// builtinPatterns returns the ordered list of generic detectors. Specific (long)
// patterns come first so they win against shorter overlapping ones (e.g. CLABE
// before generic credit card).
func builtinPatterns() []patternDef {
	return []patternDef{
		{Type: PatternCURP, Re: reCURP},
		{Type: PatternRFC, Re: reRFC},
		{Type: PatternEmail, Re: reEmail},
		{Type: PatternIBAN, Re: reIBAN},
		{Type: PatternCLABE, Re: reCLABE, Filter: func(m string) bool {
			// CLABE must be 18 dígitos, lo cual el regex ya garantiza; dejamos
			// hook por si luego queremos chequear dígito verificador.
			return len(strings.TrimSpace(m)) == 18
		}},
		{Type: PatternCreditCard, Re: reCreditCard, Filter: luhnValid},
		{Type: PatternPhone, Re: rePhone, Filter: phoneLooksReal},
		{Type: PatternAmount, Re: reAmount},
		{Type: PatternLegalName, Re: reLegalName},
		{Type: PatternAddress, Re: reAddress},
	}
}

// luhnValid is the Luhn checksum used to validate credit-card-shaped numbers.
// It also rejects matches that don't contain at least 13 digits (after stripping
// formatting), which avoids classifying CLABEs / phone numbers as cards.
func luhnValid(raw string) bool {
	digits := stripNonDigits(raw)
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	parity := len(digits) % 2
	for i, ch := range digits {
		d, err := strconv.Atoi(string(ch))
		if err != nil {
			return false
		}
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// phoneLooksReal rejects matches that are obviously not phones — too few digits,
// or all the same digit (0000000000), or starting with way-too-low country codes.
func phoneLooksReal(raw string) bool {
	digits := stripNonDigits(raw)
	if len(digits) < 10 || len(digits) > 13 {
		return false
	}
	// Repetitive digit (e.g. "0000000000") -> not a real phone.
	first := digits[0]
	allSame := true
	for i := 1; i < len(digits); i++ {
		if digits[i] != first {
			allSame = false
			break
		}
	}
	return !allSame
}

func stripNonDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
