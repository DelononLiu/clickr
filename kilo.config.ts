import { defineConfig } from '@kilocode/plugin';

export default defineConfig({
  name: 'clickr',
  type: 'static',
  build: {
    command: 'npm run build:web',
    outputDir: 'public',
    cleanInstall: true,
  },
  platform: 'kilo',
  env: {
    OPENAI_API_KEY: {
      required: false,
      description: 'OpenAI API Key',
    },
    OPENAI_BASE_URL: {
      required: false,
      default: 'https://api.openai.com/v1',
    },
    OPENAI_MODEL: {
      required: false,
      default: 'gpt-3.5-turbo',
    },
  },
});

