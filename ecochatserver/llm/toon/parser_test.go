package toon

import (
	"reflect"
	"testing"
)

func TestParseSimpleObject(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "basic key-value",
			input: "lang: en\ntext: Hello world",
			want: map[string]string{
				"lang": "en",
				"text": "Hello world",
			},
			wantErr: false,
		},
		{
			name:  "with quotes",
			input: "lang: \"ru\"\ntext: \"Привет мир\"",
			want: map[string]string{
				"lang": "ru",
				"text": "Привет мир",
			},
			wantErr: false,
		},
		{
			name:  "with markdown blocks",
			input: "```\nlang: en\ntext: Hello\n```",
			want: map[string]string{
				"lang": "en",
				"text": "Hello",
			},
			wantErr: false,
		},
		{
			name:  "with extra whitespace",
			input: "  lang:  en  \n  text:  Hello world  ",
			want: map[string]string{
				"lang": "en",
				"text": "Hello world",
			},
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "no valid pairs",
			input:   "just some text without colons",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSimpleObject(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSimpleObject() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseSimpleObject() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSimpleList(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "basic list",
			input: "Hello\nWorld\nTest",
			want: []string{"Hello", "World", "Test"},
			wantErr: false,
		},
		{
			name:  "with numbering",
			input: "1. Hello\n2. World\n3. Test",
			want: []string{"Hello", "World", "Test"},
			wantErr: false,
		},
		{
			name:  "with bullets",
			input: "- Hello\n- World\n- Test",
			want: []string{"Hello", "World", "Test"},
			wantErr: false,
		},
		{
			name:  "with markdown",
			input: "```\nHello\nWorld\n```",
			want: []string{"Hello", "World"},
			wantErr: false,
		},
		{
			name:  "with quotes",
			input: "\"Hello\"\n\"World\"",
			want: []string{"Hello", "World"},
			wantErr: false,
		},
		{
			name:  "with empty lines",
			input: "Hello\n\nWorld\n\nTest",
			want: []string{"Hello", "World", "Test"},
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "only whitespace",
			input:   "   \n   \n   ",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSimpleList(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSimpleList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseSimpleList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCleanLLMResponse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "with markdown toon",
			input: "```toon\nlang: en\ntext: Hello\n```",
			want:  "lang: en\ntext: Hello",
		},
		{
			name:  "with markdown json",
			input: "```json\n{\"key\": \"value\"}\n```",
			want:  "{\"key\": \"value\"}",
		},
		{
			name:  "with generic markdown",
			input: "```\nsome text\n```",
			want:  "some text",
		},
		{
			name:  "clean text",
			input: "lang: en\ntext: Hello",
			want:  "lang: en\ntext: Hello",
		},
		{
			name:  "with extra whitespace",
			input: "  \n  lang: en  \n  ",
			want:  "lang: en",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanLLMResponse(tt.input)
			if got != tt.want {
				t.Errorf("CleanLLMResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}
