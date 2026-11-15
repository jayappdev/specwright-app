// ESLint: catch real mistakes (undefined names, broken hook order), not style;
// Prettier handles formatting.
import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import react from "eslint-plugin-react";

export default [
  { ignores: ["dist/", ".dev/", "node_modules/", "wailsjs/", "src/*.generated.js"] },
  js.configs.recommended,
  {
    files: ["**/*.{js,jsx,mjs}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      parserOptions: { ecmaFeatures: { jsx: true } },
      globals: { ...globals.browser, ...globals.node },
    },
    plugins: { "react-hooks": reactHooks, react },
    rules: {
      "react-hooks/rules-of-hooks": "error",
      "react/jsx-uses-vars": "error", // names used only in JSX count as used
      "react/jsx-uses-react": "off",
      "no-unused-vars": ["warn", { args: "none", varsIgnorePattern: "^_", caughtErrors: "none" }],
      "no-empty": ["error", { allowEmptyCatch: true }],
    },
  },
];
