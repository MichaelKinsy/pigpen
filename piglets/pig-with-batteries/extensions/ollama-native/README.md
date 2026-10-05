# Ollama Native Extension for PiG

A PiG extension that registers an `ollama-native` provider using Ollama's native `/api/chat` endpoint for tool calling support.

## Features
- Uses Ollama native API (`/api/chat`) instead of OpenAI-compatible `/v1/chat/completions`
- Dynamic model listing from Ollama's `/api/tags` endpoint
- Respects `OLLAMA_HOST` environment variable
- Supports tool calling with models trained for it
- Streaming responses

## Configuration

Set the `OLLAMA_HOST` environment variable to use a non-default Ollama instance:

```bash
export OLLAMA_HOST=http://localhost:11434
```

## Usage

```bash
# List available models
pig --list-models ollama-native

# Use the provider
pig --model ollama-native/granite4.1:3b "Your prompt"

# Or load directly
pig -e ./extensions/ollama-native --model ollama-native/granite4.1:3b -p "Hello"
```

## Tool Calling

The extension sends PiG tools to Ollama in the correct format. When the model is prompted to use a tool, it returns a proper `tool_calls` array that PiG can execute.

### Example
```bash
pig -e ./extensions/ollama-native --model ollama-native/granite4.1:3b -p "Use calculator to compute 5*3"
```

## Requirements
- Ollama running (default: `http://localhost:11434`)
- Model with tool calling support (e.g., `granite4.1:3b`, `minicpm5-1b-uncensored`)
