"use client"

import packageJson from "../../../package.json"
import { useTranslations } from "next-intl"
import { ScrollArea } from "@/components/ui/scroll-area"
import { openUrl } from "@/lib/platform"

const CODEG_REPOSITORY = "https://github.com/spacering-net/codeg"
const CODEG_DOCUMENTATION = "https://docs.codeg.app"

/** YoYoCode 增补：「关于」页 —— 产品信息 + 对上游 Codeg 的致谢。 */
export function AboutSettings() {
  const t = useTranslations("AboutSettings")

  return (
    <ScrollArea className="h-full">
      <div className="space-y-6 p-3 md:p-4">
        <div className="flex items-center gap-4">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/icon.svg" alt="YoYoCode" className="h-16 w-16" />
          <div>
            <h3 className="text-lg font-medium">YoYoCode</h3>
            <p className="text-sm text-muted-foreground">
              v{packageJson.version}
            </p>
          </div>
        </div>

        <p className="text-sm text-muted-foreground">{t("tagline")}</p>

        <div className="rounded-md border p-3">
          <div className="space-y-1.5">
            <div className="text-sm font-medium">{t("basedOnTitle")}</div>
            <p className="text-xs text-muted-foreground">{t("basedOnText")}</p>
            <div className="flex gap-2 pt-1">
              <button
                onClick={() => void openUrl(CODEG_REPOSITORY)}
                className="inline-flex h-8 items-center rounded-md border border-input bg-background px-3 text-xs font-medium ring-offset-background transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {t("openRepository")}
              </button>
              <button
                onClick={() => void openUrl(CODEG_DOCUMENTATION)}
                className="inline-flex h-8 items-center rounded-md border border-input bg-background px-3 text-xs font-medium ring-offset-background transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {t("openDocumentation")}
              </button>
            </div>
          </div>
        </div>
      </div>
    </ScrollArea>
  )
}
