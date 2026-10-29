; Override: the inferred C++ query reports every `struct T x;` use as a type
; definition (and twice). Types are tagged only where their body is.
(namespace_definition (namespace_identifier) @name) @definition.module
(class_specifier (type_identifier) @name (field_declaration_list)) @definition.class
(struct_specifier (type_identifier) @name (field_declaration_list)) @definition.type
(union_specifier (type_identifier) @name (field_declaration_list)) @definition.type
(enum_specifier (type_identifier) @name (enumerator_list)) @definition.type
(type_definition (type_identifier) @name) @definition.type
(alias_declaration (type_identifier) @name) @definition.type
(function_definition (function_declarator (identifier) @name)) @definition.function
(function_definition (function_declarator (field_identifier) @name)) @definition.method
(function_definition (function_declarator (qualified_identifier (identifier) @name))) @definition.method
(field_declaration (function_declarator (field_identifier) @name)) @definition.method
(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
(call_expression (qualified_identifier (identifier) @name)) @reference.call
