; Override: the grammar's inferred query has functions only.
(function_definition name: (word) @name) @definition.function
(program (variable_assignment name: (variable_name) @name) @definition.variable)
(command_name (word) @name) @reference.call
