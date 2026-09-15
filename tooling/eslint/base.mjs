export function createNamingConventionRules({ allowSnakeCaseProperties = false } = {}) {
  const propertyFormats = allowSnakeCaseProperties
    ? ["camelCase", "snake_case", "UPPER_CASE"]
    : ["camelCase", "UPPER_CASE"];

  return [
    {
      selector: ["property", "objectLiteralProperty"],
      filter: {
        regex: "^(?:[A-Z][A-Za-z0-9]*)(?:-[A-Z][A-Za-z0-9]*)*$",
        match: true,
      },
      format: null,
    },
    {
      selector: ["property", "objectLiteralProperty", "typeProperty"],
      modifiers: ["requiresQuotes"],
      format: null,
    },
    {
      selector: "import",
      format: ["camelCase", "PascalCase"],
    },
    {
      selector: "typeLike",
      format: ["PascalCase"],
    },
    {
      selector: "typeProperty",
      filter: {
        regex: "^Bindings$",
        match: true,
      },
      format: ["PascalCase"],
    },
    {
      selector: "function",
      format: ["camelCase", "PascalCase"],
      leadingUnderscore: "allow",
      trailingUnderscore: "allow",
    },
    {
      selector: "enumMember",
      format: ["UPPER_CASE"],
    },
    {
      selector: "variable",
      modifiers: ["const"],
      format: ["camelCase", "UPPER_CASE"],
      leadingUnderscore: "allow",
      trailingUnderscore: "allow",
    },
    {
      selector: "variable",
      format: ["camelCase"],
      leadingUnderscore: "allow",
      trailingUnderscore: "allow",
    },
    {
      selector: ["parameter", "property", "objectLiteralProperty", "typeProperty", "method", "accessor"],
      format: propertyFormats,
      leadingUnderscore: "allow",
      trailingUnderscore: "allow",
    },
    {
      selector: "default",
      format: ["camelCase"],
      leadingUnderscore: "allow",
      trailingUnderscore: "allow",
    },
  ];
}

export function createNodeTypeCheckedConfig({
  eslint,
  globals,
  tseslint,
  tsconfigRootDir,
  project = "./tsconfig.eslint.json",
  sourceFiles = ["src/**/*.ts", "tests/**/*.ts"],
  testFiles = ["tests/**/*.ts"],
  baseGlobals = globals.node,
  extraGlobals = {},
  extraConfigs = []
} = {}) {
  return tseslint.config(
    {
      ignores: ["dist/**", "node_modules/**"]
    },
    eslint.configs.recommended,
    ...tseslint.configs.recommendedTypeChecked,
    {
      files: sourceFiles,
      languageOptions: {
        parserOptions: {
          project,
          tsconfigRootDir
        },
        globals: {
          ...baseGlobals,
          ...extraGlobals
        }
      },
      rules: {
        "@typescript-eslint/no-unused-vars": [
          "error",
          {
            argsIgnorePattern: "^_"
          }
        ],
        "@typescript-eslint/explicit-function-return-type": "off",
        "@typescript-eslint/explicit-module-boundary-types": "off",
        "@typescript-eslint/restrict-template-expressions": "off",
        "@typescript-eslint/restrict-plus-operands": "off",
        "@typescript-eslint/no-unsafe-member-access": "off",
        "no-case-declarations": "off",
        semi: ["error", "always"]
      }
    },
    ...(testFiles.length > 0
      ? [{
          files: testFiles,
          rules: {
            "@typescript-eslint/no-unsafe-assignment": "off",
            "@typescript-eslint/no-unsafe-call": "off",
            "@typescript-eslint/unbound-method": "off"
          }
        }]
      : []),
    ...extraConfigs
  );
}
