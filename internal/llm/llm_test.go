package llm

import (
	"testing"
)

func TestSanitizeJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Pure JSON Array",
			input:    `[{"title": "t1", "body": "b1"}]`,
			expected: `[{"title": "t1", "body": "b1"}]`,
		},
		{
			name:     "Markdown Wrapped JSON",
			input:    "```json\n[{\"title\": \"t1\", \"body\": \"b1\"}]\n```",
			expected: `[{"title": "t1", "body": "b1"}]`,
		},
		{
			name:     "With Leading and Trailing text",
			input:    `Here is the data: [{"title": "t1", "body": "b1"}] and some ending.`,
			expected: `[{"title": "t1", "body": "b1"}]`,
		},
		{
			name:     "No Brackets",
			input:    `invalid text`,
			expected: `invalid text`,
		},
		{
			name:     "Mismatched Brackets",
			input:    `]invalid[`,
			expected: `]invalid[`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeJSON(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeJSON() = %q, want %q", got, tt.expected)
			}
		})
	}
}
