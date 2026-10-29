; Override: the inferred C query treats every `struct foo x;` USE as a type
; definition. A tagged type here must carry its body.
(function_definition (function_declarator (identifier) @name)) @definition.function
(function_definition (pointer_declarator (function_declarator (identifier) @name))) @definition.function
(struct_specifier (type_identifier) @name (field_declaration_list)) @definition.type
(union_specifier (type_identifier) @name (field_declaration_list)) @definition.type
(enum_specifier (type_identifier) @name (enumerator_list)) @definition.type
(type_definition (type_identifier) @name) @definition.type
(preproc_def (identifier) @name) @definition.constant
(preproc_function_def (identifier) @name) @definition.function
(translation_unit (declaration (init_declarator (identifier) @name)) @definition.variable)
(translation_unit (declaration (identifier) @name) @definition.variable)
(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
