package booxpipeline

import (
	"testing"
)

func TestParseTodoResponse_SingleTodo(t *testing.T) {
	resp := `{"type":"todo","text":"Buy milk"}`
	todos := parseTodoResponse(resp)
	if len(todos) != 1 {
		t.Fatalf("got %d todos, want 1", len(todos))
	}
	if todos[0].Text != "Buy milk" {
		t.Errorf("text = %q, want 'Buy milk'", todos[0].Text)
	}
	if todos[0].Type != "todo" {
		t.Errorf("type = %q, want 'todo'", todos[0].Type)
	}
}

func TestParseTodoResponse_MultipleTodos(t *testing.T) {
	resp := `{"type":"todo","text":"First item"}
{"type":"todo","text":"Second item"}
{"type":"todo","text":"Third item"}`
	todos := parseTodoResponse(resp)
	if len(todos) != 3 {
		t.Fatalf("got %d todos, want 3", len(todos))
	}
	if todos[1].Text != "Second item" {
		t.Errorf("second text = %q, want 'Second item'", todos[1].Text)
	}
}

func TestParseTodoResponse_MixedContent(t *testing.T) {
	resp := `I found some red text on this page:
{"type":"todo","text":"Call dentist"}
No other red passages found.`
	todos := parseTodoResponse(resp)
	if len(todos) != 1 {
		t.Fatalf("got %d todos, want 1", len(todos))
	}
	if todos[0].Text != "Call dentist" {
		t.Errorf("text = %q, want 'Call dentist'", todos[0].Text)
	}
}

func TestParseTodoResponse_EmptyResponse(t *testing.T) {
	todos := parseTodoResponse("")
	if len(todos) != 0 {
		t.Errorf("got %d todos from empty response, want 0", len(todos))
	}
}

func TestParseTodoResponse_NoRedText(t *testing.T) {
	resp := `There are no red passages on this page.`
	todos := parseTodoResponse(resp)
	if len(todos) != 0 {
		t.Errorf("got %d todos, want 0", len(todos))
	}
}

func TestParseTodoResponse_EmptyText(t *testing.T) {
	// JSON with empty text should be skipped
	resp := `{"type":"todo","text":""}`
	todos := parseTodoResponse(resp)
	if len(todos) != 0 {
		t.Errorf("got %d todos, want 0 (empty text should be skipped)", len(todos))
	}
}

func TestParseTodoResponse_InvalidJSON(t *testing.T) {
	resp := `{"type":"todo","text":"valid"}
{broken json}
{"type":"todo","text":"also valid"}`
	todos := parseTodoResponse(resp)
	if len(todos) != 2 {
		t.Fatalf("got %d todos, want 2 (invalid JSON line skipped)", len(todos))
	}
}
