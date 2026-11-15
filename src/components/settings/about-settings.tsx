"use client"

import packageJson from "../../../package.json"
import { useTranslations } from "next-intl"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { openSettingsWindow } from "@/lib/api"
import { openUrl } from "@/lib/platform"
import { AboutLogoAnimation } from "./about-logo-animation"

const FORK_REPOSITORY = "https://github.com/ijry/YoYoCode"
const FORK_RELEASES = `${FORK_REPOSITORY}/releases`
const FORK_ISSUES = `${FORK_REPOSITORY}/issues`
const UPSTREAM_REPOSITORY = "https://github.com/spacering-net/codeg"
const DOCUMENTATION = "https://ijry.github.io/yoyocode-docs"
const MCODE_DOWNLOAD_URL = "https://getmcode.lingyun.net"

interface CardLink {
  label: string
  href?: string
  onSelect?: () => void
}

/** One link group. Keeping the fork, the mobile app, and upstream in separate
 * cards makes it obvious which project a link actually belongs to. */
function LinkCard({
  title,
  description,
  links,
}: {
  title: string
  description: string
  links: CardLink[]
}) {
  return (
    <section className="rounded-md border p-3">
      <div className="space-y-1.5">
        <div className="text-sm font-medium">{title}</div>
        <p className="text-xs text-muted-foreground">{description}</p>
        <div className="flex flex-wrap gap-2 pt-1">
          {links.map((link) => (
            <Button
              key={link.label}
              variant="outline"
              size="sm"
              className="text-xs"
              onClick={() => {
                if (link.href) void openUrl(link.href)
                else link.onSelect?.()
              }}
            >
              {link.label}
            </Button>
          ))}
        </div>
      </div>
    </section>
  )
}

/** YoYoCode "About" page — product info, this fork, mobile pairing, upstream. */
export function AboutSettings() {
  const t = useTranslations("AboutSettings")

  return (
    <ScrollArea className="h-full">
      <div className="space-y-6 p-3 md:p-4">
        <div className="flex items-center gap-4">
          <AboutLogoAnimation
            className="size-20 rounded-xl"
            label={t("logoAnimationLabel")}
          />
          <div>
            <h3 className="text-lg font-medium">YoYoCode</h3>
            <p className="text-sm text-muted-foreground">
              v{packageJson.version}
            </p>
          </div>
        </div>

        <p className="text-sm text-muted-foreground">{t("tagline")}</p>

        <LinkCard
          title={t("forkTitle")}
          description={t("forkText")}
          links={[
            { label: t("openForkRepository"), href: FORK_REPOSITORY },
            { label: t("openReleases"), href: FORK_RELEASES },
            { label: t("openIssues"), href: FORK_ISSUES },
            { label: t("openDocumentation"), href: DOCUMENTATION },
          ]}
        />

        <LinkCard
          title={t("appTitle")}
          description={t("appText")}
          links={[
            { label: t("downloadApp"), href: MCODE_DOWNLOAD_URL },
            {
              label: t("openWebServiceSettings"),
              onSelect: () => {
                openSettingsWindow("web-service").catch((err) => {
                  console.error("Failed to open Web Service settings", err)
                })
              },
            },
          ]}
        />

        <LinkCard
          title={t("basedOnTitle")}
          description={t("basedOnText")}
          links={[{ label: t("openRepository"), href: UPSTREAM_REPOSITORY }]}
        />
      </div>
    </ScrollArea>
  )
}
