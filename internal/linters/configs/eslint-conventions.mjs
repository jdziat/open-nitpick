export default [{
  files: ["**/*.js", "**/*.mjs", "**/*.cjs", "**/*.jsx"],
  languageOptions: { parserOptions: { ecmaFeatures: { jsx: true } } },
  linterOptions: { noInlineConfig: true },
  rules: {
    eqeqeq: ["error", "always"],
    "no-restricted-syntax": ["error",
      {
        selector: "ClassDeclaration[id]:not([id.name=/^[A-Z][A-Za-z0-9]*$/])",
        message: "Name classes in PascalCase."
      },
      {
        selector: "ClassExpression[id]:not([id.name=/^[A-Z][A-Za-z0-9]*$/])",
        message: "Name classes in PascalCase."
      }
    ]
  }
}];
