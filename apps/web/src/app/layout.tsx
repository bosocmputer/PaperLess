import type { Metadata, Viewport } from "next";
import localFont from "next/font/local";
import "./globals.css";

// Sarabun — the Thai government standard typeface. Reads cleanly in Thai and
// Latin, signals officialdom/trust (fits the legal e-signature product).
// Self-hosted woff2 (committed under ./fonts/sarabun) instead of next/font/google
// so the on-prem Docker build (`--build` on the LAN server, no internet) is
// fully offline and deterministic. Each file is the full Thai+Latin glyph set.
const sarabun = localFont({
  variable: "--font-sans",
  display: "swap",
  src: [
    { path: "./fonts/sarabun/Sarabun-Light.woff2", weight: "300", style: "normal" },
    { path: "./fonts/sarabun/Sarabun-Regular.woff2", weight: "400", style: "normal" },
    { path: "./fonts/sarabun/Sarabun-Medium.woff2", weight: "500", style: "normal" },
    { path: "./fonts/sarabun/Sarabun-SemiBold.woff2", weight: "600", style: "normal" },
    { path: "./fonts/sarabun/Sarabun-Bold.woff2", weight: "700", style: "normal" },
  ],
});

export const metadata: Metadata = {
  title: "PaperLess — ระบบเซ็นเอกสาร",
  description: "ระบบเซ็นเอกสารอิเล็กทรอนิกส์",
  manifest: "/manifest.json",
  appleWebApp: {
    capable: true,
    statusBarStyle: "default",
    title: "PaperLess",
  },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 1,
  userScalable: false,
  themeColor: "#2e498a",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="th">
      <body className={`${sarabun.variable} antialiased`}>
        {children}
      </body>
    </html>
  );
}
