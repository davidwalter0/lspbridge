; Override: adds mod, impl, type aliases, and splits methods (in impl/trait)
; from free functions.
(source_file (function_item (identifier) @name) @definition.function)
(mod_item (declaration_list (function_item (identifier) @name) @definition.function))
(impl_item (declaration_list (function_item (identifier) @name) @definition.method))
(trait_item (declaration_list (function_item (identifier) @name) @definition.method))
(trait_item (declaration_list (function_signature_item (identifier) @name) @definition.method))
(struct_item (type_identifier) @name) @definition.type
(enum_item (type_identifier) @name) @definition.type
(trait_item (type_identifier) @name) @definition.interface
(type_item (type_identifier) @name) @definition.type
(mod_item (identifier) @name) @definition.module
(const_item (identifier) @name) @definition.constant
(static_item (identifier) @name) @definition.constant
(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
(call_expression (scoped_identifier (identifier) @name)) @reference.call
(macro_invocation (identifier) @name) @reference.call
(impl_item (type_identifier) @name . (declaration_list)) @definition.class
