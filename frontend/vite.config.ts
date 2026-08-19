import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // 不强制端口：5173 被其他进程占用时自动递增到可用端口，避免 dev 启动失败。
    proxy: {
      '/api': 'http://127.0.0.1:8030'
    }
  },
  build: {
    // antd v5 tree-shaking 后单独成块仍 ~1.1MB，这是该库的固有体积，无法再拆。
    // 500K 是 Vite 针对"应做代码分割的应用代码"的默认值，对叶子依赖的 UI 库
    // 无指导意义。核心目标——让频繁变动的应用代码主 chunk 极小化（已从 ~722K
    // 降至 ~25K）——已经达成，故放宽阈值消除对 vendor 库的误报警告。
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        // 把重量级第三方依赖拆成独立 vendor chunk：
        // - 浏览器可长期缓存（依赖更新才变），应用代码改动不强制重新下载
        // - 主 chunk 从 ~722K 降到 ~25K
        // - antd 与 icons 分开，任一方升级不连累另一方缓存失效
        manualChunks: {
          'vendor-react': ['react', 'react-dom', 'react-router-dom'],
          'vendor-query': ['@tanstack/react-query'],
          'vendor-antd': ['antd'],
          'vendor-icons': ['@ant-design/icons']
        }
      }
    }
  }
});
