// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

import React from "react"
import { ReactComponent as Globe } from "src/assets/icons/globe.svg"
import { ReactComponent as Home } from "src/assets/icons/home.svg"
import { NodeData } from "src/types"
import Card from "src/ui/card"

// TODO: apply readonly value where needed

export default function ServeView({
  readonly,
  node,
}: {
  readonly: boolean
  node: NodeData
}) {
  return (
    <>
      <h1 className="mb-1">Share local content</h1>
      <p className="description mb-5">
        Share local ports, services, and content to your Tailscale network or to
        the broader internet.{" "}
        <a
          href="https://tailscale.com/kb/1312/serve"
          className="text-blue-700"
          target="_blank"
          rel="noreferrer"
        >
          Learn more &rarr;
        </a>
      </p>
      <ShareCard readonly={readonly} />
    </>
  )
}

function ShareCard({ readonly }: { readonly: boolean }) {
  return (
    <Card noPadding className="-mx-5 p-5">
      <p className="font-medium leading-snug">Target</p>
      <p className="mt-1 text-gray-500 text-sm leading-tight">
        The content you want to share.
      </p>
      {/* radix dropdown item */}
      <p className="mt-6 font-medium leading-snug">Share</p>
      <div className="mt-2.5 flex flex-col gap-2.5">
        <ShareRadioButton
          title="Within your tailnet"
          description="Everyone within your tailnet can access (Tailscale Serve)."
          icon={<Home />}
          readonly={readonly}
        />
        <ShareRadioButton
          title="On the internet"
          description="Anyone with the URL can access (Tailscale Funnel)."
          icon={<Globe />}
          readonly={readonly}
        />
      </div>
      <p className="mt-6 font-medium leading-snug">Preview destination URL</p>
      <p className="mt-3 text-gray-500 text-sm leading-tight">
        The URL where your content will be available.
      </p>
    </Card>
  )
}

function ShareRadioButton({
  title,
  description,
  icon,
  readonly,
}: {
  title: string
  description: string
  icon: React.ReactNode
  readonly: boolean
}) {
  return (
    <label className="flex mt-[10px]">
      <input
        type="radio"
        name={`${title}-radio`}
        className="radio mt-1"
        disabled={readonly}
        //   checked={selectedRole === "owner"}
        //   onChange={changeRole("owner")}
      />
      <div className="ml-3">
        <div className="flex items-center">
          {icon}
          <span className="ml-2 text-gray-800 leading-snug">{title}</span>
        </div>
        <div className="text-gray-500 text-sm leading-tight">{description}</div>
      </div>
    </label>
  )
}
