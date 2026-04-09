package handler

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// cyrillicToLatin maps Cyrillic characters (Uzbek + Russian) to Latin equivalents.
var cyrillicToLatin = map[rune]string{
	// Uzbek Cyrillic
	'А': "A", 'а': "a",
	'Б': "B", 'б': "b",
	'В': "V", 'в': "v",
	'Г': "G", 'г': "g",
	'Д': "D", 'д': "d",
	'Е': "E", 'е': "e",
	'Ё': "Yo", 'ё': "yo",
	'Ж': "J", 'ж': "j",
	'З': "Z", 'з': "z",
	'И': "I", 'и': "i",
	'Й': "Y", 'й': "y",
	'К': "K", 'к': "k",
	'Л': "L", 'л': "l",
	'М': "M", 'м': "m",
	'Н': "N", 'н': "n",
	'О': "O", 'о': "o",
	'П': "P", 'п': "p",
	'Р': "R", 'р': "r",
	'С': "S", 'с': "s",
	'Т': "T", 'т': "t",
	'У': "U", 'у': "u",
	'Ф': "F", 'ф': "f",
	'Х': "X", 'х': "x",
	'Ц': "Ts", 'ц': "ts",
	'Ч': "Ch", 'ч': "ch",
	'Ш': "Sh", 'ш': "sh",
	'Щ': "Sh", 'щ': "sh",
	'Ъ': "", 'ъ': "",
	'Ы': "I", 'ы': "i",
	'Ь': "", 'ь': "",
	'Э': "E", 'э': "e",
	'Ю': "Yu", 'ю': "yu",
	'Я': "Ya", 'я': "ya",
	// Uzbek-specific
	'Ў': "O", 'ў': "o",
	'Қ': "Q", 'қ': "q",
	'Ғ': "G", 'ғ': "g",
	'Ҳ': "H", 'ҳ': "h",
}

// transliterateToLatin converts Cyrillic characters to Latin equivalents,
// keeps existing Latin chars and digits, replaces everything else with underscore.
func transliterateToLatin(s string) string {
	var result strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if latin, ok := cyrillicToLatin[r]; ok {
			result.WriteString(latin)
		} else if unicode.IsSpace(r) || r == '-' || r == '_' {
			result.WriteRune('_')
		}
		// Other characters (punctuation, other scripts) are silently dropped
	}
	return result.String()
}

// generateCodeFromName creates a database-safe code from a name/title.
// It transliterates Cyrillic to Latin, keeps only [A-Z0-9_], and applies a max length.
// If the result is empty, it uses the provided fallback.
func generateCodeFromName(name string, maxLen int, fallback string) string {
	code := transliterateToLatin(strings.TrimSpace(name))
	code = strings.ToUpper(code)

	// Collapse multiple underscores
	for strings.Contains(code, "__") {
		code = strings.ReplaceAll(code, "__", "_")
	}
	code = strings.Trim(code, "_")

	if len(code) > maxLen {
		code = code[:maxLen]
	}
	code = strings.TrimRight(code, "_")

	if code == "" {
		code = fallback
	}
	return code
}

// generateCodeFromNameLower is like generateCodeFromName but returns lowercase.
func generateCodeFromNameLower(name string, maxLen int, fallback string) string {
	code := transliterateToLatin(strings.TrimSpace(name))
	code = strings.ToLower(code)

	// Collapse multiple underscores
	for strings.Contains(code, "__") {
		code = strings.ReplaceAll(code, "__", "_")
	}
	code = strings.Trim(code, "_")

	if len(code) > maxLen {
		code = code[:maxLen]
	}
	code = strings.TrimRight(code, "_")

	if code == "" {
		code = fallback
	}
	return code
}

// generateTenantCode creates a tenant code from a company name with a random suffix.
func generateTenantCode(companyName string, randomSuffix func() string) string {
	base := generateCodeFromNameLower(companyName, 40, fmt.Sprintf("tenant_%d", time.Now().Unix()))
	return fmt.Sprintf("%s_%s", base, randomSuffix())
}
