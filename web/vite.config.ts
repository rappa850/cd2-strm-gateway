import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import UnoCSS from 'unocss/vite'
export default defineConfig({plugins:[vue(),UnoCSS()],server:{proxy:{'/api':'http://localhost:8080','/r':'http://localhost:8080'}}})
