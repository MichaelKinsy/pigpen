// Dumps the tables of tree-sitter-bash 0.25.1's generated parser (src/parser.c) into the binary
// blob bashparse embeds (bashparse/internal/ts/bash_tables.bin). Build and run:
//   cc -I<tree-sitter-bash>/src -o dumptables dumptables.c && ./dumptables <out.bin>
// The parser.c of the npm package tree-sitter-bash@0.25.1 is #included, so every table is the
// compiler's own reading of the generated initializers (no regex over C). All integers are
// little-endian. Layout (the Go loader, bashparse/internal/ts/tables.go, reads it in this order):
//   u32 magic 'TSB1'; u32 x 11 counts: symbol_count token_count external_token_count state_count
//   large_state_count production_id_count field_count max_alias_sequence_length keyword_capture_token
//   action_count small_table_len
//   symbol_names (u16 len, bytes) x symbol_count
//   public_symbol_map u16 x symbol_count
//   symbol_metadata (u8 visible, u8 named, u8 supertype) x symbol_count
//   field_names (u16 len, bytes) x (field_count+1)
//   field_map_slices (u16 index, u16 length) x production_id_count
//   u32 field_entry_count; field_map_entries (u16 field_id, u8 child_index, u8 inherited) x count
//   alias_sequences u16 x production_id_count*max_alias_sequence_length
//   primary_state_ids u16 x state_count
//   lex_modes (u16 lex_state, u16 external_lex_state) x state_count
//   parse_table u16 x large_state_count*symbol_count
//   small_parse_table u16 x small_table_len
//   small_parse_table_map u32 x (state_count-large_state_count)
//   parse_actions: 8 raw bytes x action_count
//   external_scanner_symbol_map u16 x external_token_count
//   external_scanner_states u8 x 131*external_token_count   (rows = u32 prefixed)
//   (supertypes are not exposed by the node API the original reads)
#include <stdio.h>
#include <stddef.h>
#include <string.h>
#include "parser.c"

static FILE *out;
static void w8(unsigned v) { fputc(v & 0xff, out); }
static void w16(unsigned v) { w8(v); w8(v >> 8); }
static void w32(unsigned v) { w16(v); w16(v >> 16); }
static void wstr(const char *s) { size_t n = strlen(s); w16((unsigned)n); fwrite(s, 1, n, out); }

int main(int argc, char **argv) {
  const TSLanguage *l = tree_sitter_bash();
  _Static_assert(sizeof(TSParseActionEntry) == 8, "action entry size");
  _Static_assert(offsetof(TSParseAction, shift.state) == 2, "shift.state");
  _Static_assert(offsetof(TSParseAction, shift.extra) == 4, "shift.extra");
  _Static_assert(offsetof(TSParseAction, shift.repetition) == 5, "shift.repetition");
  _Static_assert(offsetof(TSParseAction, reduce.child_count) == 1, "reduce.child_count");
  _Static_assert(offsetof(TSParseAction, reduce.symbol) == 2, "reduce.symbol");
  _Static_assert(offsetof(TSParseAction, reduce.dynamic_precedence) == 4, "reduce.dynamic_precedence");
  _Static_assert(offsetof(TSParseAction, reduce.production_id) == 6, "reduce.production_id");
  _Static_assert(offsetof(TSParseActionEntry, entry.count) == 0, "entry.count");
  _Static_assert(offsetof(TSParseActionEntry, entry.reusable) == 1, "entry.reusable");
  _Static_assert(sizeof(TSLexerMode) == 6, "lexer mode");
  out = fopen(argv[1], "wb");
  w32(0x31425354);
  unsigned action_count = sizeof(ts_parse_actions) / sizeof(ts_parse_actions[0]);
  unsigned small_len = sizeof(ts_small_parse_table) / sizeof(ts_small_parse_table[0]);
  w32(l->symbol_count); w32(l->token_count); w32(l->external_token_count); w32(l->state_count);
  w32(l->large_state_count); w32(l->production_id_count); w32(l->field_count);
  w32(l->max_alias_sequence_length); w32(l->keyword_capture_token); w32(action_count); w32(small_len);
  for (unsigned i = 0; i < l->symbol_count; i++) wstr(l->symbol_names[i]);
  for (unsigned i = 0; i < l->symbol_count; i++) w16(l->public_symbol_map[i]);
  for (unsigned i = 0; i < l->symbol_count; i++) { w8(l->symbol_metadata[i].visible); w8(l->symbol_metadata[i].named); w8(l->symbol_metadata[i].supertype); }
  for (unsigned i = 0; i <= l->field_count; i++) wstr(l->field_names[i] ? l->field_names[i] : "");
  for (unsigned i = 0; i < l->production_id_count; i++) { w16(l->field_map_slices[i].index); w16(l->field_map_slices[i].length); }
  unsigned fe = sizeof(ts_field_map_entries) / sizeof(ts_field_map_entries[0]);
  w32(fe);
  for (unsigned i = 0; i < fe; i++) { w16(l->field_map_entries[i].field_id); w8(l->field_map_entries[i].child_index); w8(l->field_map_entries[i].inherited); }
  for (unsigned i = 0; i < l->production_id_count * l->max_alias_sequence_length; i++) w16(l->alias_sequences[i]);
  for (unsigned i = 0; i < l->state_count; i++) w16(l->primary_state_ids[i]);
  for (unsigned i = 0; i < l->state_count; i++) { w16(l->lex_modes[i].lex_state); w16(l->lex_modes[i].external_lex_state); w16(l->lex_modes[i].reserved_word_set_id); }
  for (unsigned i = 0; i < l->large_state_count * l->symbol_count; i++) w16(l->parse_table[i]);
  for (unsigned i = 0; i < small_len; i++) w16(l->small_parse_table[i]);
  for (unsigned i = 0; i < l->state_count - l->large_state_count; i++) w32(l->small_parse_table_map[i]);
  for (unsigned i = 0; i < action_count; i++) fwrite(&l->parse_actions[i], 1, 8, out);
  for (unsigned i = 0; i < l->external_token_count; i++) w16(l->external_scanner.symbol_map[i]);
  unsigned rows = sizeof(ts_external_scanner_states) / sizeof(ts_external_scanner_states[0]);
  w32(rows);
  for (unsigned i = 0; i < rows * l->external_token_count; i++) w8(l->external_scanner.states[i]);
  fclose(out);
  fprintf(stderr, "ok: %u symbols %u states %u actions small=%u fieldentries=%u extrows=%u\n", l->symbol_count, l->state_count, action_count, small_len, fe, rows);
  return 0;
}
